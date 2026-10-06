## Why

The bridge only writes messages to `store/messages.db` when they arrive as whatsmeow events (`handleMessage`, `handleHistorySync`). A message sent through `/api/send` is delivered, but `sendWhatsAppMessage` never touches the `MessageStore`, and WhatsApp does not echo a message back to the device that sent it. So `list_messages`, `get_last_interaction` and `list_chats` show no trace of what the agent has just sent, and an agent that checks its own work concludes the send failed and may send it again. Now that `/api/send` also carries `mentions` and media with captions, the gap affects every kind of send the MCP server offers.

## What Changes

- After `client.SendMessage` succeeds, the bridge writes the sent message to the `messages` table using the server-assigned message ID and timestamp from the `SendResponse`, with `is_from_me = 1` and the user's own phone-number user (`client.Store.ID.User`) as sender, so the MCP server shows it as "Me".
- The chat row is created if missing and its `last_message_time` is set to the same timestamp as the stored message, so `list_chats` orders it first and shows it as the last message; an existing resolved chat name is kept.
- Text, mention (`ExtendedTextMessage`) and media sends (image, audio, video, document) are all stored, reusing `extractTextContent` and `extractMediaInfo`, so captions and the media metadata (URL, keys, hashes, length) are recorded and `download_media` works on the user's own attachments.
- Sent messages are stored under the phone-number chat JID (LID recipients resolved with `resolveLID`), matching how received messages are stored.
- Storage is idempotent with any later echo of the same message from another linked device or history sync (same `(id, chat_jid)` key).
- A failed send stores nothing; a storage failure after a successful send is logged and does not turn the API response into a failure.
- No change to the `/api/send` request or response format, and no change to the Python MCP server is required.

## Capabilities

### New Capabilities
- `outgoing-message-storage`: recording messages sent through the bridge's REST API in the local message store, including chat bookkeeping, sender identity, media metadata, idempotency with echoes and failure handling.

### Modified Capabilities
<!-- None: no specs exist yet under openspec/specs/. -->

## Impact

- **Code**: `whatsapp-bridge/main.go` — `sendWhatsAppMessage` (keep the `SendResponse`, call a new store helper), `startRESTServer` (pass `messageStore` and a logger through), possibly a small schema-initialisation helper on `MessageStore` so tests can use an in-memory database. New Go tests in `whatsapp-bridge/`.
- **Data**: more rows in `messages` and fresher `chats.last_message_time`; no schema change and no migration.
- **APIs**: `/api/send` contract unchanged; behaviour of `list_messages`, `list_chats`, `get_last_interaction`, `get_message_context` and `download_media` improves for own sends.
- **Docs**: `CHANGELOG.md` entry (UK English); README note if it documents the old limitation.
- **Credits**: approach adapted from `daymade/whatsapp-mcp` commit "fix(bridge): store the messages we send" and upstream PRs lharries/whatsapp-mcp #229 and #265.
