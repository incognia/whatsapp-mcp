## Why

The bridge only stores messages; to learn that "Amelia wrote", that "someone mentioned me in the DevSecOps group" or that a message contains "guardia", the user has to ask an MCP client to poll `list_messages`. Nothing outside the bridge can react to a message as it arrives. A small, safe notification path — listeners that match live messages and POST them to a webhook — turns the bridge into a real-time source for automations (n8n, Home Assistant, a local script, a phone push service) without giving anything new the ability to send on the account.

## What Changes

- Add **message listeners**: named, persisted rules with match criteria — chat JID, sender, text `contains`, text `regex`, `mentions_me` — combined by a per-listener match mode (`any` = OR, `all` = AND), with messages sent by the account itself excluded unless the listener opts in.
- Evaluate listeners only for **live** messages (`events.Message`) after they are stored, on the normalised data the bridge already produces: phone-number JIDs from `resolveLID`, text from `extractTextContent` (captions included) with `resolveLIDMentions` applied, and mentions read from `ContextInfo.MentionedJID`, which may hold LIDs. History-sync messages, edits and messages older than a configurable age never fire a listener.
- Add **webhook delivery**: a JSON `POST` per match with a stable payload (message ID, chat JID and name, sender JID and name, timestamp, content, media type, `is_from_me`, listener ID and name), an HMAC-SHA256 signature with a timestamp header when the listener has a secret, a per-request timeout and bounded retries with backoff, run by a bounded asynchronous worker queue so whatsmeow's event handler is never blocked. A full queue drops the job and counts it, rather than blocking.
- Add **bridge REST endpoints** to create, list, get, update (enable/disable) and delete listeners, plus a validate-only endpoint and a "send test delivery" endpoint. Invalid listeners are rejected with a `400` listing every problem. Secrets are write-only: they are never returned or logged.
- Add **SSRF and exfiltration safeguards**: webhook URLs restricted to `http`/`https` with no embedded credentials, `https` required for public hosts, link-local, metadata, multicast and unspecified addresses and the bridge's own API address always refused (checked again at connection time), an optional strict host allowlist `WEBHOOK_ALLOWED_HOSTS`, no redirects followed; listener management refuses browser-originated requests (an `Origin` header, a non-JSON body or a foreign `Host`) and, when the API is bound beyond loopback, requires `WEBHOOK_ADMIN_TOKEN`.
- Add **MCP tools** in `whatsapp-mcp-server` — `create_listener`, `list_listeners`, `delete_listener`, `set_listener_enabled`, `test_listener` — that call the bridge endpoints; `list_listeners` includes each listener's last delivery outcome.
- Persist listeners and a short delivery log in `store/messages.db` through a new, versioned schema migration (`PRAGMA user_version`), the first one this fork has.
- Document the feature in `README.md` and record it in `CHANGELOG.md`.
- **Non-goals:** automatic replies or any other action that sends on the WhatsApp account (it raises the risk of an unofficial client being banned); SSE or MCP resource notifications; a web UI; auto-downloading media on match; reactions, receipts, presence or group-membership events; a configuration-file source of listeners (discussed as an alternative in `design.md`).

Delivery is split into two phases, both inside this change: **Phase 1** — bridge schema, matching, delivery worker, REST API and Go tests; **Phase 2** — MCP tools, README and CHANGELOG. Phase 1 is usable on its own through `curl`.

## Capabilities

### New Capabilities
- `message-listeners`: defining, validating, storing and managing listeners (REST and MCP), and deciding which live messages match them — criteria, OR/AND semantics, LID-aware mentions, own-message and history-sync exclusion.
- `webhook-delivery`: turning a match into an HTTP `POST` — payload shape, signature, timeouts, retries, the bounded queue and its drop policy, the delivery log, and the URL safety rules.

### Modified Capabilities
<!-- None: openspec/specs/ is empty, so there are no existing capabilities to modify. -->

## Impact

- **Code:** `whatsapp-bridge/main.go` (hook in `handleMessage`, schema migration in `NewMessageStore`, new routes in `startRESTServer`, worker start-up and shutdown in `main`); new files in `package main`, e.g. `listeners.go`, `webhook_delivery.go`, `listeners_api.go`, and their `_test.go` files; `whatsapp-mcp-server/whatsapp.py` and `main.py`.
- **APIs:** new `/api/listeners`, `/api/listeners/{id}`, `/api/listeners/validate`, `/api/listeners/{id}/test` and `/api/listeners/{id}/deliveries` routes on the existing loopback REST server. Existing endpoints are unchanged.
- **Data:** new tables `listeners` and `listener_deliveries` in `store/messages.db`; existing tables untouched. The Python MCP server reads `messages.db` directly and is unaffected by the new tables.
- **Configuration:** new optional environment variables `WEBHOOK_ALLOWED_HOSTS`, `WEBHOOK_ADMIN_TOKEN`, `WEBHOOK_QUEUE_SIZE`, `WEBHOOK_WORKERS`, `WEBHOOK_TIMEOUT`, `WEBHOOK_MAX_AGE`.
- **Dependencies:** none new; Go standard library only (`net/http`, `crypto/hmac`, `regexp`, `net`).
- **Security:** a new outbound network path that carries message content. It is opt-in (no listener, no traffic) and bounded by the safeguards above.
- **Related changes:** `store-sent-messages` may make the bridge store messages it sends; listeners exclude the account's own messages by default, so that change does not cause unwanted deliveries.
- **Prior art:** the listener and match-mode model follows `AdamRussak/whatsapp-mcp` (branches `webhook` and `trigger-mode`, ADR 0001); the simple register/notify shape follows upstream PR #326; SSE (#183, #191) is a considered alternative.
