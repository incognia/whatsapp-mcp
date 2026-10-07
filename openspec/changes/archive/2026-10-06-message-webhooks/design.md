## Context

See `proposal.md` (Why) for motivation and the two specs (`specs/message-listeners/spec.md`, `specs/webhook-delivery/spec.md`) for the behaviour contract. This document covers how the bridge implements it.

Current state of `whatsapp-bridge` (one `package main`, `main.go`, about 1,700 lines):

- `client.AddEventHandler` in `main()` dispatches `*events.Message` to `handleMessage` and `*events.HistorySync` to `handleHistorySync`. whatsmeow calls the handler synchronously on its event path, so slow work there delays every later event.
- `handleMessage` already rewrites `msg.Info.Chat` and `msg.Info.Sender` to phone-number JIDs (`resolveLID`, or `SenderAlt` when the sender is a LID), computes the chat name (`GetChatName`), extracts text with `extractTextContent` (captions and ephemeral/view-once envelopes included, via `unwrapMessage`), rewrites LID mentions in that text (`resolveLIDMentions`) and stores the message with `StoreMessage`. Messages without text and media are skipped before storage.
- `NewMessageStore` creates `chats` and `messages` with `CREATE TABLE IF NOT EXISTS`; there is no schema version. The DSN already enables foreign keys.
- `startRESTServer` registers `/api/send` and `/api/download` on `http.DefaultServeMux`, binds to `127.0.0.1:8080` unless `BIND_ADDR` says otherwise, and has no authentication.
- Tests are table-driven `_test.go` files in `package main` (`caption_test.go`, `media_url_test.go`), run with `go test ./...`. Go 1.26, whatsmeow `v0.0.0-20260929112325`.
- `whatsapp-mcp-server` is a FastMCP server: `whatsapp.py` holds the REST helpers (`requests.post` to `http://localhost:8080/api`), `main.py` the `@mcp.tool()` wrappers.

Prior art studied:

- **AdamRussak/whatsapp-mcp** (branches `webhook` and `trigger-mode`, merged; ADR 0001, schema v5). A listener is a `webhook_configs` row with child `webhook_triggers` rows (`all`, `chat_jid`, `sender`, `keyword`, `media_type`, each `exact`/`contains`/`regex`), an OR/AND `trigger_mode`, HMAC `X-Webhook-Signature`, 5 retries with 1–16 s backoff, a `webhook_logs` table, versioned additive migrations, LID aliases expanded at load time, and save-time validation rules R1–R6 that reject listeners that can never match. Delivery runs as one unbounded goroutine per match that sleeps between retries; the secret is returned by the API; the signature does not cover a timestamp; the payload is logged in full.
- **lharries/whatsapp-mcp PR #326** "Add webhook notifications for new messages": a JSON file of URLs, `register_webhook`/`list_webhooks`/`unregister_webhook` MCP tools, a fire-and-forget `POST` of every message with a 5 s timeout. No filtering, no signature, no retries, no URL checks.
- **PR #183** "Add SSE endpoint and MCP resource notifications" and **PR #191** "SSE pub/sub stream": an `/api/events` Server-Sent Events broadcaster, with the MCP server turning events into `resources/updated` notifications.

## Goals / Non-Goals

**Goals:**

- Matching runs on exactly the data the bridge stores, so what a listener sees is what `list_messages` later shows.
- The whatsmeow event path does a bounded, in-memory amount of extra work per message: no SQL, no DNS, no network.
- A listener cannot be planted by a web page or a LAN neighbour, and a planted or mistaken URL cannot reach link-local or metadata addresses or the bridge itself.
- Small enough to review in one sitting: standard library only, a handful of new files, no UI.

**Non-Goals:**

- Guaranteed delivery. The queue lives in memory; deliveries in flight when the bridge stops are lost. Receivers that need completeness can reconcile through `list_messages`.
- Criteria beyond the five in the spec (media type, "replies to me", reactions, time windows). The schema leaves room for them.
- Encrypting secrets at rest. `messages.db` already holds every message in clear text; a secret beside it adds no new class of exposure. File permissions on `store/` are the control.

## Decisions

### D1. Listener evaluation hooks into `handleMessage`, after storage

A new call `listeners.Evaluate(ev)` runs at the end of `handleMessage`, only when `StoreMessage` succeeded, with an `IncomingMessage` value built from what `handleMessage` already computed: chat JID, sender user and JID, chat name, content (after `resolveLIDMentions`), media type, filename, timestamp, `IsFromMe`, plus `msg.IsEdit` and the raw mention list. `handleHistorySync` never calls it, which is how history sync is excluded by construction rather than by a flag.

Filters applied before criteria, cheapest first: no enabled listeners → return; `IsEdit` → return; chat `status@broadcast` → return; timestamp older than `WEBHOOK_MAX_AGE` → return; then per listener: disabled → skip; `IsFromMe && !include_from_me` → skip.

*Alternative considered:* evaluating in `StoreMessage` (would also see history sync and need a flag) or in a separate event handler registered with `AddEventHandler` (would repeat the LID resolution and text extraction, and could disagree with what was stored).

### D2. A flat criteria object with list values, and literal OR/AND across criteria

A listener has five optional criteria (`chat_jids`, `senders`, `contains`, `regex`, `mentions_me`) and a `match_mode` of `or` or `and` over the set ones. Values inside one list are always alternatives.

This differs from AdamRussak's trigger rows on purpose. With one row per value, "chat A or chat B, from Amelia" cannot be one AND listener, and "chat = A AND chat = B" is a listener that can never match — which is why ADR 0001 needed validation rules R3–R5 and a live validate call in the UI. Grouping values per field removes that whole class of impossible listeners, keeps the MCP tool signature simple (a model can fill `senders=[...]`, `contains=[...]` directly), and keeps "AND means AND" honest. What remains to validate is syntax (D6). The match-mode names (`or`/`and`), the default (`or`), and refusing unknown values instead of treating them as OR are taken from ADR 0001.

Criterion details:

- `contains`: `strings.Contains(strings.ToLower(text), lowerNeedle)`, with needles lower-cased once at load time. Case-insensitive only; no accent folding (see Open Questions).
- `regex`: compiled once at load (`regexp.Compile`, RE2, so no catastrophic backtracking); a 500-character limit bounds compile cost.
- `senders`: compares against both `sender` (user part) and the full sender JID, so `5215512345678` and `5215512345678@s.whatsapp.net` both work.
- `matched` in the payload is built in the same pass that decides the match (one function returns the matched criterion names; the decision is "non-empty" for OR and "equals the set criteria" for AND). ADR 0001's review found that two separate loops could disagree; one function avoids that.

*Alternatives considered:* AdamRussak's trigger rows plus R1–R6 (more expressive per row, but needs the impossibility checks); a boolean expression language (needs a parser; several listeners already cover it); "OR within type, AND across types" implicitly (rejected in ADR 0001 because the UI word "AND" would lie — here the per-field grouping is explicit in the data shape, so it does not).

### D3. Identity: normalise on save, compare as strings on match

Save time: `normaliseChat` and `normaliseSender` strip `+`, spaces and dashes from bare numbers, drop device parts, and map `@lid` values through `client.Store.LIDs.GetPNForLID` (as `resolveLID` does). Match time compares strings only, because `handleMessage` has already resolved the message's chat and sender. This is the reverse of AdamRussak's approach (expand each trigger to its LID aliases at load), and it is possible because our fork stores everything under phone-number JIDs.

A LID with no known mapping is stored as given; it still matches if the message also arrives with that unresolved LID, which is the same thing the stored data would show.

### D4. "Mentions me" reads the mention list, not the text

`extractMentionedJIDs(msg *waProto.Message) []string` unwraps the message (`unwrapMessage`) and collects `ContextInfo.GetMentionedJID()` from the extended text, image, video, document, audio and sticker sub-messages (whichever carries a `ContextInfo`). A JID matches "me" when, after parsing and dropping the device part, its user equals `client.Store.ID.User` on `s.whatsapp.net` or `client.Store.LID.User` on `lid`. Both own identities are read once per evaluation from the device store, so the check works whether or not the mention's LID is in the LID map — the account's own LID is always known to the device.

Reading the rewritten text (`@5215512345678`) was rejected: it depends on the mapping being known, and the text form also appears when someone types a number without tagging.

`GroupMentions` (mentions of a whole sub-group) and `NonJIDMentions` are ignored for now.

### D5. In-memory snapshot, swapped atomically

The `ListenerRegistry` holds an immutable `[]*compiledListener` behind `atomic.Pointer`. Every successful create/update/delete in the API writes SQLite first, then reloads all listeners from SQLite, compiles them and swaps the pointer. Evaluation reads the pointer once; no lock is taken on the event path. With at most 50 listeners, a full reload per write is simpler than incremental updates and cannot drift from the database.

### D6. Storage: two tables and the first versioned migration

`NewMessageStore` gains `migrate(db)`, which reads `PRAGMA user_version` and applies numbered steps in one transaction each, then sets `user_version`. Version 1:

```sql
CREATE TABLE IF NOT EXISTS listeners (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    match_mode TEXT NOT NULL DEFAULT 'or' CHECK (match_mode IN ('or', 'and')),
    chat_jids TEXT NOT NULL DEFAULT '[]',   -- JSON array of normalised JIDs
    senders TEXT NOT NULL DEFAULT '[]',     -- JSON array of phone numbers / JIDs
    contains TEXT NOT NULL DEFAULT '[]',    -- JSON array of strings
    regex TEXT NOT NULL DEFAULT '',
    mentions_me INTEGER NOT NULL DEFAULT 0,
    include_from_me INTEGER NOT NULL DEFAULT 0,
    webhook_url TEXT NOT NULL,
    secret TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS listener_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    listener_id INTEGER NOT NULL REFERENCES listeners(id) ON DELETE CASCADE,
    delivery_id TEXT NOT NULL,
    event TEXT NOT NULL,                 -- 'message' | 'test'
    message_id TEXT,
    chat_jid TEXT,
    status TEXT NOT NULL,                -- 'delivered' | 'failed' | 'dropped'; written once, when the outcome is known
    attempts INTEGER NOT NULL DEFAULT 0,
    status_code INTEGER,
    error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_listener_deliveries_listener ON listener_deliveries(listener_id, id DESC);
```

Lists are JSON text columns rather than a child table: they are always read together, never queried by value, and a single row keeps create/update atomic without joins. Delivery rows are written by the worker, never on the event path (a `dropped` record is written by the worker that notices the drop counter, or by a small logging goroutine — see D7). Pruning (`DELETE … WHERE id <= (SELECT id … ORDER BY id DESC LIMIT 1 OFFSET 1000)`) runs after each completed delivery, at most once a minute.

Only additive DDL is allowed in migrations, so an older binary keeps working against a newer database (it ignores the new tables). The changes `store-sent-messages` and `per-chat-history-sync` may also need schema steps; whichever lands first introduces `migrate`, and the others append their version in merge order.

*Alternatives considered:* AdamRussak's three-table model (`webhook_configs`, `webhook_triggers`, `webhook_logs`) with a `schema_migrations` table; a JSON file like PR #326 (no transactions, one more file to protect, and no delivery log).

### D7. Delivery: bounded queue, fixed workers, timer-based retries

```
handleMessage ──Evaluate──▶ match? ──non-blocking send──▶ jobs (chan, cap WEBHOOK_QUEUE_SIZE)
                                   └─ full → drops++ (atomic)            │
                                                                         ▼
                                                    WEBHOOK_WORKERS goroutines: attempt()
                                                       ├─ 2xx → record delivered
                                                       ├─ retryable & attempts<4 → time.AfterFunc(backoff, requeue)
                                                       └─ else → record failed
```

- The job carries the pre-marshalled payload body, listener ID, URL, secret, delivery ID (16 random bytes, hex) and attempt count. The body is marshalled on the event path once per match; that is CPU-only and small (content capped at 4,096 characters).
- Retries are re-enqueued from `time.AfterFunc` with a non-blocking send; if the queue is full at that moment the job is recorded as `dropped`. No worker sleeps.
- The `http.Client` has `Timeout: WEBHOOK_TIMEOUT`, `CheckRedirect` returning `http.ErrUseLastResponse` (a 3xx is then a non-retryable failure), a `Transport` with `Proxy: nil` (an environment proxy would bypass the IP check), and a `net.Dialer` whose `Control` hook rejects forbidden IP addresses after DNS resolution (D8). Response bodies are read through `io.LimitReader(…, 4096)` and discarded.
- A per-process LRU set of `(listener_id, chat_jid, message_id)` (capacity 4,096) implements "at most once per listener and message"; it is checked and updated inside `Evaluate`.
- Dropped deliveries are counted atomically on the event path; a once-a-minute ticker goroutine logs the count and writes `dropped` rows, so the event path never touches SQLite.
- Shutdown: `main` already waits on SIGINT/SIGTERM; it will call `deliverer.Shutdown(5*time.Second)`, which closes intake, cancels pending retry timers and waits for workers up to the deadline.

*Alternatives considered:* one goroutine per delivery with `time.Sleep` between retries (AdamRussak, PR #326) — unbounded under a burst or a dead receiver, and invisible when it piles up; a persistent outbox table polled by workers — survives restarts but puts SQLite writes on every match and needs its own cleanup; neither fits a personal bridge where a missed notification can be recovered from `messages.db`.

### D8. Security model

The threat is not a remote attacker on the REST API (it is loopback-bound) but things that can reach loopback: a web page in the user's browser (CSRF, DNS rebinding), other local processes, and a LAN when `BIND_ADDR` is widened. A planted listener is a quiet, persistent exfiltration channel for every future message, which is worse than a one-off `/api/send`.

1. **Management endpoints** (spec "Protection of the management endpoints"): refuse any `Origin` header (browsers always send it on cross-origin `POST`/`PATCH`/`DELETE`; `curl`, Python `requests` and the MCP server never do); require `Content-Type: application/json` on bodies, which forces a CORS preflight that the bridge never approves; check `Host` against loopback names and the bind address to defeat DNS rebinding; optional `WEBHOOK_ADMIN_TOKEN` bearer token compared with `crypto/subtle.ConstantTimeCompare`, mandatory when the bind address is not loopback. These checks live in one `requireLocalAdmin` wrapper applied to every `/api/listeners` route. Extending the same wrapper to `/api/send` is tempting but out of scope (it would change existing behaviour); it is recorded as a follow-up.
2. **Target URLs** (spec "Webhook URL safety policy"): validated at save time with `net/url` plus `net.ParseIP` where the host is a literal; `https` required for public hosts so message content does not cross the internet in clear text; loopback and private ranges allowed over `http` because a local n8n, Home Assistant or script is the main use case. At connection time the dialer's `Control` hook rejects link-local (`169.254.0.0/16`, `fe80::/10`), unspecified, multicast, broadcast addresses and the bridge's own `bindAddr:port`, which covers DNS rebinding of the target. `WEBHOOK_ALLOWED_HOSTS`, when set, is the strictest control and is checked both at save and at dial time (by host name).
3. **Integrity and replay**: HMAC-SHA256 over `timestamp + "." + body` (the Stripe/Slack scheme) with a per-listener secret of at least 16 characters, so receivers can reject old or altered requests. AdamRussak signs only the body, which allows replay.
4. **Confidentiality of configuration**: secrets are write-only (`has_secret`), URL query values are masked in API answers, logs carry scheme and host only, the delivery log holds no content or payload.
5. **Ban risk**: listeners only read and notify. No auto-reply, read receipt, typing or presence action is triggered by a match, so the account's visible behaviour on WhatsApp does not change.

### D9. REST routing and error shape

Routes use Go 1.22+ method patterns on the default mux (`POST /api/listeners`, `GET /api/listeners/{id}`, `PATCH /api/listeners/{id}`, `DELETE /api/listeners/{id}`, `POST /api/listeners/validate`, `POST /api/listeners/{id}/test`, `GET /api/listeners/{id}/deliveries`). The literal `validate` segment takes precedence over `{id}` under the 1.22 precedence rules, so no special casing is needed. Errors follow ADR 0001's compatible shape: `{"success": false, "error": "<first message>", "errors": [{"field": "regex", "message": "…"}]}`. JSON bodies are decoded with `DisallowUnknownFields` and a 64 KiB `http.MaxBytesReader` limit, so a typo such as `"sender"` instead of `"senders"` is an error rather than a silently ignored field.

### D10. MCP tools are thin wrappers

`whatsapp.py` gains `_listener_request(method, path, json=None)`, which adds `Authorization` when `WEBHOOK_ADMIN_TOKEN` is set and returns the decoded JSON (including `errors` on `400`). `main.py` exposes `create_listener`, `list_listeners`, `delete_listener`, `set_listener_enabled` and `test_listener` with docstrings that explain OR/AND, list semantics and that own messages are excluded unless `include_from_me` is true. No validation is duplicated in Python, so the bridge stays the single source of truth (ADR 0001, §3). `create_listener` accepts `secret`, but `list_listeners` never shows it, which keeps it out of model context after creation.

### D11. Alternatives to webhooks considered

- **SSE stream (`/api/events`, PRs #183 and #191)**: good for a process that stays connected, and the basis for MCP `resources/updated` notifications. It needs a long-lived consumer, loses events while nobody is connected, and most MCP clients do not surface resource notifications to the user today. It can reuse the same `Evaluate` output later as a second sink; deferred.
- **MCP notifications only**: an MCP server runs only while a client session is open, so it cannot "tell me when Amelia writes" while the user is away.
- **Polling `messages.db`**: works today with no bridge change, but adds latency and every consumer reimplements LID handling and mention detection.
- **Listeners in a configuration file or environment variables** (AdamRussak's `webhooks.yaml.example`): simpler to review and version, and immune to API-planted listeners. It cannot be managed from an MCP client and requires a restart per change. A read-only `WEBHOOK_LISTENERS_FILE` loaded at start-up could be added later on top of the same registry; not in this change.

## Risks / Trade-offs

- [A receiver is down for hours and matches keep coming] → each delivery gives up after 4 attempts (about 45 s), the queue is bounded, and drops are logged and recorded; nothing grows without limit.
- [A regular expression or `contains` list is expensive on a busy group] → RE2 is linear-time, patterns are compiled once, and limits (500 characters, 20 entries, 50 listeners) bound the per-message cost to microseconds.
- [A listener with a broad criterion (e.g. `contains: ["a"]`) floods the receiver] → accepted: it is the user's choice; the queue bound protects the bridge, and the delivery log makes the volume visible.
- [Offline backlog after reconnecting fires stale notifications] → `WEBHOOK_MAX_AGE` (default 15 minutes) skips old messages; users who want everything can set `0`.
- [An event is delivered twice by WhatsApp, or the bridge restarts mid-retry] → in-process de-duplication covers the first; the second can lose or (if the receiver answered but the bridge crashed before recording) repeat a delivery. Receivers should de-duplicate on `message.id` or `X-Webhook-Delivery`; the README will say so.
- [The `Origin`/`Host` checks break a legitimate browser-based tool] → there is no such tool in this fork; a future UI would use the admin token and an explicit allowed-origin setting.
- [Message content leaves the machine] → only when the user creates a listener; `https` is mandatory off the local network, and the allowlist lets cautious users pin destinations.
- [The account's own LID is not yet in the device store on a fresh pairing] → `mentions_me` still matches by phone-number JID; a test covers the empty-LID case.
- [Schema migration conflicts with the other open changes] → only additive steps, and the version number is assigned at merge time (D6).

## Migration Plan

1. **Phase 1 (bridge)**: ship `migrate` with version 1, the registry, the delivery worker and the REST routes. Existing databases gain two empty tables; nothing else changes, and no traffic is sent until a listener exists.
2. **Phase 2 (MCP and docs)**: add the MCP tools, the README section and the CHANGELOG entry. Phase 2 depends only on Phase 1's REST contract.
3. **Verification**: `go test ./...`; then a manual end-to-end test with a local receiver (`python3 -m http.server`-style script that prints headers and verifies the signature), creating a listener with `curl`, sending a message from another phone, and checking `GET /api/listeners/{id}/deliveries`.
4. **Rollback**: run the previous binary. It ignores the new tables and `user_version`; listeners simply stop firing. To remove all traces, `DROP TABLE listener_deliveries; DROP TABLE listeners; PRAGMA user_version = 0;` on a stopped bridge.

## Open Questions

- Should `contains` also ignore accents ("guardía" vs "guardia")? The `accent-insensitive-contact-search` change may introduce a folding helper; if it does, `contains` can adopt it later without changing the API (it would only make more messages match).
- Should a `replies_to_me` criterion (`ContextInfo.Participant` equals the account) be added next? It fits the same `ContextInfo` reading as `mentions_me` and would be an additive field.
- Is the default `WEBHOOK_MAX_AGE` of 15 minutes right for the user's habits? It is an environment setting and can be tuned without code changes.
