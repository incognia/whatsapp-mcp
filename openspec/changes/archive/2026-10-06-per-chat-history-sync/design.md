## Context

See `proposal.md` (Why) for motivation and `specs/history-backfill/spec.md` for the behaviour contract. Current state relevant to the approach, verified in the code:

- `whatsapp-bridge/main.go` already stores every `*events.HistorySync` through `handleHistorySync`, which resolves LID chats and senders with `resolveLID`, rewrites mentions with `resolveLIDMentions`, extracts captions with `extractTextContent` and media metadata with `extractMediaInfo(msg, msgID, msgTime)`. It does not look at the sync type, so an `ON_DEMAND` batch would already be stored, but:
  - it takes `messages[0]` as the "latest" message and calls `StoreChat`, which is `INSERT OR REPLACE INTO chats`; for an older batch this would move `last_message_time` backwards and reorder `list_chats`;
  - it never reports anything back to a caller.
- `messages` has `PRIMARY KEY (id, chat_jid)` and `StoreMessage` uses `INSERT OR REPLACE`, so re-delivered messages are already idempotent. Chats and messages are keyed by phone-number JIDs after the LID migration (`migrateLIDChats`).
- `requestHistorySync(client)` exists but has no callers, calls `client.BuildHistorySyncRequest(nil, 100)` (a `nil` dereference: the builder reads `lastKnownMessageInfo.Chat` unconditionally) and sends to `status@s.whatsapp.net` without `Peer: true`. It cannot work and is removed.
- The REST server binds to `127.0.0.1` by default and has no authentication; the MCP server talks to it via `WHATSAPP_API_BASE_URL = "http://localhost:8080/api"` in `whatsapp-mcp-server/whatsapp.py`.
- `go-sqlite3` scans `TIMESTAMP` columns into `time.Time`, so the anchor timestamp can be read directly (PR #364 parsed text with several layouts; that is unnecessary here).

### whatsmeow API (verified in `go.mau.fi/whatsmeow@v0.0.0-20260929112325-8b41cfe6d9c4`)

- `send.go`: `func (cli *Client) BuildHistorySyncRequest(lastKnownMessageInfo *types.MessageInfo, count int) *waE2E.Message` builds a `ProtocolMessage` of type `PEER_DATA_OPERATION_REQUEST_MESSAGE` with `PeerDataOperationRequestType_HISTORY_SYNC_ON_DEMAND` and a `HistorySyncOnDemandRequest{ChatJID: info.Chat.String(), OldestMsgID: info.ID, OldestMsgFromMe: info.IsFromMe, OnDemandMsgCount: count, OldestMsgTimestampMS: info.Timestamp.Unix()}` (the field says "MS" but holds seconds). Its doc comment: send with `Client.SendPeerMessage`; the response comes as `*events.HistorySync` with type `ON_DEMAND`, holding up to `count` messages immediately before the given message; 50 per request is recommended.
- `send.go`: `func (cli *Client) SendPeerMessage(ctx, message) (SendResponse, error)` is exactly `cli.SendMessage(ctx, ownID.ToNonAD(), message, SendRequestExtra{Peer: true})` and returns `ErrNotLoggedIn` when there is no own ID. For this request type `sendPeerMessage` sets `push_priority="high_force"` and `privacy_sensitive="1"`, so the phone is woken.
- `message.go`: the downloaded blob is unmarshalled into `waHistorySync.HistorySync`; `GetSyncType()` returns `waHistorySync.HistorySync_ON_DEMAND` for these answers. Each `Conversation` may carry `EndOfHistoryTransferType` (`COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY`, `COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY`, `COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS`, …). whatsmeow also stores PN↔LID mappings found in the blob before emitting the event.
- `store.LIDStore` offers `GetLIDForPN(ctx, pn)` and `GetPNForLID(ctx, lid)` through `client.Store.LIDs`.
- `HistorySyncOnDemandRequest` also has `AccountLid` and `SupportInlineResponse` fields that the builder leaves unset; we leave them unset too.

## Goals / Non-Goals

**Goals:**

- One small, explicit per-chat request path built on the verified whatsmeow API, reusing `handleHistorySync` for storage.
- A status model that lets the MCP tool give a useful answer within one tool call in the common case, without blocking the bridge.
- Conservative limits by default.

**Non-Goals:**

- A "general" history sync for all chats (LukasHaas' `/api/sync` without a chat) or a full-history (`FULL_HISTORY_SYNC_ON_DEMAND`) request.
- Automatically walking a chat back to a target date (PR #364's `backfill.py`). The MCP tool can be called repeatedly; a loop helper can come later.
- Persisting request status across bridge restarts, or retrying requests automatically.
- Reconnect robustness from the LukasHaas fork (`events.Disconnected`, `KeepAliveTimeout` logging); worth a separate change.
- Recovering chats that have no stored message at all.

## Decisions

### D1. Endpoint shape: `POST /api/history/backfill` + `GET` status on the same path

JSON body (`chat_jid`, `count`) like `/api/send` and `/api/download`, rather than PR #364's `GET /api/backfill?chat=…&count=…`: a `GET` that triggers network traffic to the phone is unsafe to retry or prefetch. The `GET` on the same path is read-only status. Responses use the existing `success`/`message` JSON convention plus the fields listed in the spec. Implemented as `handleHistoryBackfill(w, r)` registered in `startRESTServer`, delegating to testable functions.

### D2. Anchor from `messages.db`

New `MessageStore.GetOldestMessage(chatJID string) (id string, ts time.Time, fromMe bool, err error)`:

```sql
SELECT id, timestamp, is_from_me FROM messages
WHERE chat_jid = ? AND id != ''
ORDER BY timestamp ASC, id ASC LIMIT 1
```

`sql.ErrNoRows` maps to the `no_anchor` 404. The anchor is converted to `types.MessageInfo{MessageSource: {Chat: requestJID, IsFromMe: fromMe, IsGroup: …}, ID: id, Timestamp: ts}`; only `Chat`, `ID`, `IsFromMe` and `Timestamp` are read by the builder, so the sender is not needed (PR #364 filled it; harmless but unused).

Messages that the bridge never stored (no text and no media, e.g. reactions, protocol messages) cannot be anchors; the oldest stored message is good enough because the phone returns messages strictly before the anchor and duplicates are harmless.

*Alternative considered*: LukasHaas' synthetic anchor (`ID: "FFFFFFFFFFFFFFFF"`, `Timestamp: now`) for chats with no messages. Rejected: it is undocumented behaviour, the phone may silently ignore an unknown message ID, and a chat with zero stored messages is better served by waiting for one live message and then backfilling. The spec therefore returns `no_anchor`.

### D3. LID vs phone-number addressing

Input normalisation (`normaliseBackfillJID`): parse with `types.ParseJID`, drop device (`ToNonAD`), accept only `DefaultUserServer`, `HiddenUserServer` and `GroupServer`, reject `status@broadcast`, `BroadcastServer`, `NewsletterServer` and others. The **storage JID** is `resolveLID(client, jid)` (phone number when known). The **request JID** is, for a phone-number user JID, `client.Store.LIDs.GetLIDForPN(ctx, pn)` when non-empty, else the phone-number JID; for groups, the group JID.

Rationale: recent WhatsApp clients key one-to-one chats by LID (history sync conversations increasingly arrive as `@lid`, which is why `handleHistorySync` resolves them), so the phone is most likely to find the anchor under the LID. The response reports `request_jid`, so a failed attempt can be diagnosed. Because whatever comes back is passed through `resolveLID`, storage is the same either way.

*Alternative*: always send the phone-number JID (what PR #364 and LukasHaas do, written before LID addressing was widespread). Kept as the fallback. Which form the phone honours is verified manually during implementation (task 6.3); if only the phone-number form works, flipping the preference is a one-line change that does not alter the spec's observable contract beyond `request_jid`.

**Outcome (task 6.3, 2026-10-06):** the LID form works. On the live account, two requests for a one-to-one chat addressed by its LID were answered (44 messages in 0.7 s, then 19 messages after about 50 s), and two group requests were answered by the group JID (47 and 20 messages). The LID default is kept. The phone-number form was not exercised, because forcing it needs a temporary code change and the default already works; it remains the fallback for chats whose LID is unknown.

### D4. Sending

`client.SendPeerMessage(ctx, client.BuildHistorySyncRequest(info, count))` with a 30-second context. This is the documented route and equals `SendMessage(ctx, ownJID.ToNonAD(), msg, SendRequestExtra{Peer: true})` used by PR #364. LukasHaas' fork omits `Peer: true`, which sends a normal message to one's own number; we do not copy that. Before sending: `client.IsConnected()` and `client.IsLoggedIn()`/`client.Store.ID != nil`, else 503. Send errors map to 502 and the tracker entry is not created.

### D5. Asynchronous model and status tracker

An in-memory `backfillTracker` (`sync.Mutex`, `map[string]*backfillStatus` keyed by storage JID, plus `lastAccepted time.Time`) holds: `ChatJID`, `RequestJID`, `Count`, `AnchorID`, `AnchorTimestamp`, `RequestedAt`, `Status` (`pending`/`completed`/`timed_out`), `CompletedAt`, `MessagesStored`, `OldestTimestamp`, `MoreAvailable *bool`. It is passed to both `startRESTServer` and the event handler (or held in a package-level variable initialised in `main`, matching the file's style).

Completion: `handleHistorySync` gains awareness of the sync type. When `historySync.Data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND`, after storing each conversation it calls `tracker.complete(chatJID, stored, oldest, moreAvailable)` where `moreAvailable` comes from `conversation.EndOfHistoryTransferType` only when the field is set (the enum's zero value is a real value, so presence must be checked): `…BUT_MORE_MSG_REMAIN_ON_PRIMARY` → true, `…NO_MORE_MESSAGE_REMAIN…` and `…BUT_NO_ACCESS` → false, absent → nil. An `ON_DEMAND` batch for a chat with no pending entry is stored normally and ignored by the tracker. The oldest timestamp reported is re-read with `GetOldestMessage` after storing.

Timeout is evaluated lazily: when the status is read or a new request is checked, a `pending` entry older than 120 s becomes `timed_out`. No goroutines or timers.

MCP side: `request_chat_history` posts, then, if `wait_seconds > 0`, polls `GET` every 2 s until not pending or the wait is over, and returns a dict (`success`, `status`, `chat_jid`, `request_jid`, `count`, `oldest_known_timestamp`, `messages_stored`, `more_available`, `message`). 20 s default keeps a typical round trip (PR #364's script waits 12 s) inside one call while staying under common MCP client timeouts.

*Alternatives*: blocking the HTTP request until the event arrives (ties up a handler, fragile with client timeouts, and the answer may never come); fire-and-forget only, as in PR #364 and LukasHaas (the agent cannot tell success from silence). Chosen: accept-then-poll.

### D6. Storage changes for older batches

- `StoreChat` never lowers `last_message_time` and never blanks a name: it reads the existing row (`SELECT name, last_message_time FROM chats WHERE jid = ?`), keeps the later of the two `time.Time` values and the non-empty name, then writes with `INSERT … ON CONFLICT(jid) DO UPDATE`. The comparison is done in Go rather than with SQL `MAX`, because `go-sqlite3` stores timestamps as text with a UTC offset and text comparison is wrong across offsets (e.g. after a time-zone change). This also fixes the same latent problem for ordinary history syncs that arrive out of order.
- `handleHistorySync` computes the chat's latest timestamp as the maximum over the conversation's messages instead of trusting `messages[0]`.
- Message storage is unchanged (`INSERT OR REPLACE` on `(id, chat_jid)`), which satisfies idempotency.

### D7. Limits and ban-risk posture

- `count`: default 50 (whatsmeow's recommendation), accepted 1–200 (PR #364 defaults to 200; we allow it but do not default to it).
- Per chat: refuse while `pending` or within 30 s of the last request; globally: 5 s between accepted requests. 429 with `Retry-After`. Constants `backfillDefaultCount`, `backfillMaxCount`, `backfillChatCooldown`, `backfillGlobalCooldown`, `backfillTimeout` at the top of the new code.
- Rationale: whatsmeow is an unofficial client; on-demand requests are peer messages to the user's own phone (not to contacts), which is low-risk compared with sending, but bursts of high-priority wake-ups are unusual traffic. The README will say to use the feature sparingly. No hourly cap for now; the cooldowns already bound traffic to at most 720 requests per hour in theory, and real use is a handful.

### D8. Code placement

New code lives in a new file `whatsapp-bridge/history_backfill.go` (package `main`): JID normalisation, anchor-to-`MessageInfo` conversion, tracker, rate limiter and HTTP handler. `main.go` only registers the route, initialises the tracker and calls the tracker from `handleHistorySync`. This keeps `main.go` from growing and makes unit tests (`history_backfill_test.go`) straightforward with an in-memory SQLite `MessageStore` and an injectable clock. Functions that need `*whatsmeow.Client` take small function parameters (`lidForPN`, `pnForLID`, `send`) so tests avoid a live client.

## Risks / Trade-offs

- [The phone ignores the request (offline, chat deleted on the phone, anchor not found, wrong JID form)] → status becomes `timed_out`; the response shows `request_jid` and the anchor; README explains the phone must be online.
- [Unofficial client, possible account flagging] → conservative defaults, cooldowns, no automatic loops, README warning.
- [The anchor is a message the phone no longer has (deleted for everyone, or bad data from a past sync)] → the phone may return nothing; documented. A later enhancement could retry with the next-oldest message.
- [Several chats answered in one `ON_DEMAND` blob, or a blob for a chat without a pending request] → handled per conversation; unmatched conversations are stored and ignored by the tracker.
- [`StoreChat` change affects all callers] → the new semantics (never move time backwards, never blank a name) are strictly safer; covered by tests. Coordinate with `store-sent-messages`, which may also call `StoreChat`.
- [In-memory status is lost on restart] → acceptable: messages are already stored; status is only a convenience.
- [Large batches (200) produce verbose `logger.Infof` lines per message] → unchanged behaviour; acceptable.

## Migration Plan

No schema change and no data migration. Deploy by rebuilding the bridge and restarting the MCP server. Rollback: revert the commit; messages already backfilled remain valid rows. The removed `requestHistorySync` had no callers.

## Open Questions

- ~~Whether the phone answers more reliably to the LID or the phone-number form of a one-to-one chat (D3)~~: resolved in task 6.3; the LID form works (see D3).
- Whether setting `SupportInlineResponse` / `AccountLid` would make the phone answer inline through `PeerDataOperationRequestResponseMessage` instead of a history sync notification; whatsmeow does not handle such inline answers today, so they stay unset.

## Credits

Approach adapted from upstream PR lharries/whatsapp-mcp #364 ("Add on-demand history backfill for a chat": anchor on the oldest stored message, `Peer: true` send, asynchronous storage through the existing history handler) and from the `LukasHaas/whatsapp-mcp` fork (`/api/sync` endpoint, `requestChatSync`, JSON request body).
