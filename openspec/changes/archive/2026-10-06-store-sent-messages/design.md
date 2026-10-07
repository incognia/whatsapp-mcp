## Context

See `proposal.md` (Why) for the motivation and `specs/outgoing-message-storage/spec.md` for the required behaviour.

Current state of `whatsapp-bridge/main.go` relevant to this change:

- `sendWhatsAppMessage(client, recipient, message, mediaPath, mentions)` builds a `waProto.Message` (`Conversation`, `ExtendedTextMessage` with `ContextInfo.MentionedJID`, or an `Image`/`Audio`/`Video`/`DocumentMessage` filled from the `client.Upload` response), then calls `client.SendMessage` and discards the returned `whatsmeow.SendResponse`. It receives neither the `MessageStore` nor a logger.
- `startRESTServer(client, messageStore, port)` already holds the `MessageStore` but does not pass it to the send path. `main` owns `logger`.
- Received messages go through `handleMessage`: `resolveLID` on chat and sender, `GetChatName`, `StoreChat` (INSERT OR REPLACE, which rewrites `name` and `last_message_time`), `extractTextContent` + `resolveLIDMentions`, `extractMediaInfo(msg, id, timestamp)`, then `StoreMessage` (INSERT OR REPLACE on the `(id, chat_jid)` primary key; silently skips rows with no content and no media).
- History sync stores own messages with `sender = client.Store.ID.User`; the Python MCP server prints `is_from_me` rows as "Me" and uses `is_from_me = 1 AND sender = <number>` to render "@Me" mentions.
- `list_chats` in `whatsapp-mcp-server/whatsapp.py` finds a chat's last message by joining on `chats.last_message_time = messages.timestamp`, so both columns must hold the identical value.
- `messages.chat_jid` has a foreign key on `chats.jid` (`_foreign_keys=on`), so the chat row must exist before the message is inserted.
- `NewMessageStore` hard-codes `file:store/messages.db`, which makes database-backed unit tests awkward; existing tests (`caption_test.go`, `media_url_test.go`, `media_filename_test.go`) only exercise pure helpers.

## Goals / Non-Goals

**Goals:**
- Store every successful `/api/send` (text, mentions, media with or without caption) with exactly the same shape a received message has, so every read path in the MCP server treats it identically.
- Keep the send path's success/failure semantics and the REST contract unchanged.
- Make the storage step unit-testable without a WhatsApp connection.

**Non-Goals:**
- Changing what is sent on the wire (for example, adding `FileName` to outgoing `DocumentMessage`; sent documents will be stored with the generated `document_<time>_<id>` filename, as received documents without a filename already are).
- Storing messages sent from other devices or by other code paths (`requestHistorySync`'s peer message is not user content).
- Delivery/read receipts, message status tracking, or retries.
- Any change to the Python MCP server or to the database schema.

## Decisions

### 1. Store inside the bridge, right after `client.SendMessage` succeeds
`sendWhatsAppMessage` keeps the `SendResponse` and, only when `err == nil`, calls a new storage step. Every early `return false, ...` (bad JID, refused or unreadable `media_path`, upload failure, Ogg analysis failure, send error) therefore happens before any write, which satisfies "failed sends are not stored" by construction.

*Alternatives considered:* (a) insert in the Python MCP server after a successful HTTP response — rejected: the server has no message ID or server timestamp, would duplicate the Go storage logic, and would miss other REST clients; (b) rely on WhatsApp echoing the message back as an event — rejected: whatsmeow does not emit an event for messages sent by the same device, which is the bug itself; (c) store before sending and roll back on error — rejected: no real message ID exists before the send, and a crash would leave phantom rows.

### 2. Use the server-assigned ID and timestamp
The row uses `resp.ID` and `resp.Timestamp` (falling back to `time.Now()` only if the timestamp is zero). The same `time.Time` value is passed to `StoreChat` and `StoreMessage`, so the `list_chats` last-message join matches, and an echo carrying the same ID maps onto the same primary key.

### 3. Mirror `handleMessage` for addressing, naming and content
- Chat JID: `resolveLID(client, recipientJID)` (which also applies `ToNonAD()`), matching how received messages are keyed and how `migrateLIDChats` normalises old rows.
- Chat name: `GetChatName(client, messageStore, chatJID, chatJID.String(), nil, chatJID.User, logger)`. It returns an existing non-empty name unchanged, otherwise resolves contact or group names exactly as for incoming messages. The `sender` fallback argument is the recipient's user, not our own, so a new one-to-one chat is never named after the account owner.
- Chat row: `StoreChat(chatJID, name, timestamp)` before `StoreMessage`, satisfying the foreign key and updating `last_message_time`. Because the preserved name is passed back in, the INSERT OR REPLACE does not lose it.
- Content: `extractTextContent(msg)` (covers `Conversation`, `ExtendedTextMessage` and captions via `unwrapMessage`). Outgoing mentions are already written as `@<phone number>`, so `resolveLIDMentions` is unnecessary but harmless; it is not applied.
- Media: `extractMediaInfo(msg, resp.ID, timestamp)`, which reads URL, media key, hashes and length from the outgoing protobuf that was filled from the upload response, and generates the same `<type>_<time>_<id suffix>` filename as for received media. This keeps `download_media` working for own attachments.
- Sender: `client.Store.ID.User` with `is_from_me = true`, matching history sync. If `client.Store.ID` is nil (should not happen while connected) the sender is stored as an empty string rather than skipping the row.

*Alternative considered:* the `EnsureChat` (INSERT OR IGNORE) + `TouchChatLastMessageTime` pair from upstream PR #229 — rejected because `GetChatName` already preserves existing names and the fork already uses `StoreChat` everywhere; adding two more store methods would create a second, divergent chat-bookkeeping path.

### 4. Testable seam: a client-free `storeSentMessage` helper
Split the work so the database part does not need a `*whatsmeow.Client`:

- `sendWhatsAppMessage` resolves the client-dependent inputs (chat JID, chat name, own user) and calls
  `storeSentMessage(store *MessageStore, chatJID types.JID, chatName, sender, msgID string, timestamp time.Time, msg *waProto.Message) error`.
- `storeSentMessage` calls `StoreChat`, `extractTextContent`, `extractMediaInfo` and `StoreMessage`, and returns the first error.
- `NewMessageStore()` delegates to a new `openMessageStore(dsn string)` holding the existing schema creation, so tests can open `file:<t.TempDir()>/messages.db?_foreign_keys=on`. A file in a temporary directory is preferred over `:memory:`, because `database/sql` pools connections and each `:memory:` connection would see its own empty database.

Signatures change to `sendWhatsAppMessage(client, messageStore, recipient, message, mediaPath, mentions, logger)` and `startRESTServer(client, messageStore, port, logger)`; `main` passes its `logger`.

### 5. Storage errors are logged, never surfaced as send failures
If `storeSentMessage` fails, `sendWhatsAppMessage` logs `logger.Warnf("Failed to store sent message: %v", err)` and still returns `true`. The message is already delivered; a failure response would invite the agent to send it again (the exact behaviour this change is trying to stop). On success it prints the same `[time] → sender: ...` console line `handleMessage` prints for own messages.

### 6. Idempotency relies on the existing primary key
No de-duplication logic is added: `StoreMessage` is INSERT OR REPLACE on `(id, chat_jid)`. An echo from another linked device or a later history sync carries the same ID and, after `resolveLID`, the same chat JID, so it overwrites the row with equivalent data (`is_from_me` true, sender the own user) instead of adding a second one.

## Risks / Trade-offs

- [Echo stored under an unresolved LID chat while the send was stored under the phone-number chat, giving two rows] → both paths call `resolveLID`; if the mapping was unknown at echo time, the existing startup `migrateLIDChats` merges LID chats into phone-number chats and drops duplicates.
- [Echo with a different timestamp moves `last_message_time` slightly] → harmless; the latest writer wins and the message timestamp in the same write keeps the `list_chats` join consistent.
- [Media send whose upload response lacks a URL or keys] → the row is still stored with `media_type` and filename; `download_media` would fail for it exactly as it does today for incomplete received media. The upload response normally carries all fields, so this is not expected.
- [Audio and other media with no caption] → `StoreMessage` only skips rows with neither content nor media type; media rows always have a type, so they are kept.
- [`GetChatName` for a new group performs a network call (`GetGroupInfo`) on the send path] → adds latency only for the first send to an unknown group; same behaviour as receiving.
- [Database locked or write failure] → logged; the message stays delivered and will appear if WhatsApp later echoes it or history sync replays it.
- [Concurrent writes from the event handler and the REST handler] → SQLite serialises writes and both use INSERT OR REPLACE with equivalent data; no extra locking is introduced.

## Migration Plan

No schema change and no data migration. Deploy by rebuilding and restarting the bridge (`go build` in `whatsapp-bridge/`). Messages sent before the upgrade remain absent unless history sync supplies them. Rollback is reverting the commit; rows already written are valid ordinary rows and need no clean-up.

## Credits

The approach is adapted from `daymade/whatsapp-mcp` commit `ae6a423` "fix(bridge): store the messages we send" (and its documentation commit `7d86ea5` "docs: record that sent messages are never stored locally"), and from upstream pull requests lharries/whatsapp-mcp #229 "Persist text outbounds locally so single-device-account readers see own-sends" (foreign-key and chat-ordering analysis) and #265 "persist outgoing messages". This fork differs from them in using `extractMediaInfo(msg, msgID, msgTime)` for generated filenames, LID resolution, the `mentions` path and a client-free helper for tests.
