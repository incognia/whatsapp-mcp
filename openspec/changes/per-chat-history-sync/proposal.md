## Why

The bridge only ever receives message history once: in the initial history sync that WhatsApp pushes when the device is paired. If a chat's history arrives incomplete, is older than that window, or was stored with bad data, the only remedy today is to unlink and re-pair the device (which the user has already had to do once). WhatsApp supports an on-demand history request for a single chat, and whatsmeow exposes it, so the bridge can fetch older messages for one chat without touching the pairing.

## What Changes

- Add a bridge REST endpoint, `POST /api/history/backfill`, that asks the user's primary phone for up to `count` messages older than the oldest message already stored for one chat. It returns `202 Accepted` immediately; the messages arrive later as an `ON_DEMAND` history sync event and are stored by the existing history sync path.
- Add `GET /api/history/backfill?chat_jid=…` returning the in-memory status of the latest backfill request for that chat (pending, completed with the number of messages stored and whether more history remains on the phone, or timed out).
- Add an MCP tool, `request_chat_history(chat_jid, count=50, wait_seconds=20)`, that calls the endpoint, optionally polls the status for a short time, and reports clearly that delivery is asynchronous.
- Pick the anchor (oldest stored message ID, timestamp and `from_me`) for the chat from `messages.db`, and address the request using the chat's LID when WhatsApp knows one for a phone-number chat, falling back to the phone-number JID.
- Guard the feature: refuse when disconnected or not logged in, validate and cap `count`, and rate-limit requests per chat and globally to keep the unofficial client's traffic low.
- Make history sync storage safe for backfilled batches: messages stay idempotent (`INSERT OR REPLACE` on `(id, chat_jid)`), and a chat's `last_message_time` SHALL never move backwards when an older batch is stored.
- Replace the unused and broken `requestHistorySync` function in `whatsapp-bridge/main.go` (it passes a `nil` anchor, which panics inside `BuildHistorySyncRequest`, and sends to `status@s.whatsapp.net` instead of the user's own devices).
- Document the feature and its limits in `README.md` and record it in `CHANGELOG.md`.

No breaking changes: existing endpoints, tools and the database schema are unchanged.

## Capabilities

### New Capabilities

- `history-backfill`: on-demand retrieval of older messages for one chat through the bridge REST API and an MCP tool, covering anchor selection, LID/phone-number addressing, asynchronous delivery and status, idempotent storage, count limits, rate limiting and error handling.

### Modified Capabilities

None. There are no existing specs under `openspec/specs/`; the storage guarantees needed for backfilled batches are specified within `history-backfill`.

## Impact

- **Code**: `whatsapp-bridge/main.go` (new handler, request builder, anchor query, status tracker, changes to `handleHistorySync` and `StoreChat`, removal of `requestHistorySync`); new Go tests in `whatsapp-bridge/`; `whatsapp-mcp-server/whatsapp.py` (HTTP client functions) and `whatsapp-mcp-server/main.py` (new tool).
- **APIs**: two new REST routes on the loopback-bound bridge (`127.0.0.1:8080`); one new MCP tool.
- **Dependencies**: none new; uses `go.mau.fi/whatsmeow` `v0.0.0-20260929112325-8b41cfe6d9c4` (`Client.BuildHistorySyncRequest`, `Client.SendPeerMessage`, `Store.LIDs.GetLIDForPN`).
- **Operational**: requires the primary phone to be online; each request is a peer message to the user's own phone. Over-use of an unofficial client carries account risk, hence the limits.
- **Related work**: other in-flight changes (`store-sent-messages`, `message-webhooks`) may also touch `handleHistorySync` and `StoreChat`; whichever lands second must rebase onto the other.
- **Credits**: approach adapted from upstream PR lharries/whatsapp-mcp #364 ("Add on-demand history backfill for a chat") and the `/api/sync` endpoint in the `LukasHaas/whatsapp-mcp` fork.
