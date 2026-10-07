## 1. Testable message store

- [x] 1.1 In `whatsapp-bridge/main.go`, extract the body of `NewMessageStore()` into `openMessageStore(dsn string) (*MessageStore, error)` (schema creation unchanged) and make `NewMessageStore()` create `store/` and call it with `file:store/messages.db?_foreign_keys=on`; verify with `go build ./...` and by starting the bridge against an existing `store/messages.db` without errors

## 2. Storage helper

- [x] 2.1 Add `storeSentMessage(store *MessageStore, chatJID types.JID, chatName, sender, msgID string, timestamp time.Time, msg *waProto.Message) error` that calls `StoreChat(chatJID.String(), chatName, timestamp)`, then `extractTextContent(msg)` and `extractMediaInfo(msg, msgID, timestamp)`, then `StoreMessage(..., isFromMe = true, ...)` with the same `timestamp`, returning the first error; verify with `go vet ./...`
- [x] 2.2 Add `whatsapp-bridge/sent_message_test.go` using `openMessageStore` on a `t.TempDir()` database, with table-driven cases for: plain `Conversation`; `ExtendedTextMessage` with `ContextInfo.MentionedJID` (content keeps `@<number>`); `ImageMessage` with caption and URL/keys/hashes/length (row has `media_type = image`, caption as content, `image_<time>_<id suffix>.jpg`, all media fields); `DocumentMessage` without caption (row stored with empty content); verify `is_from_me = 1`, `sender` equals the given own user and `chats.last_message_time` equals `messages.timestamp`, with `go test ./...` passing
- [x] 2.3 Add tests to `sent_message_test.go` for chat bookkeeping and idempotency: storing into a new chat creates the chat row (no foreign-key error); storing into an existing chat named `Alice` with an older time keeps the name passed in and updates `last_message_time`; storing the same `msgID`/chat twice, and then a simulated echo via `StoreMessage` with the same key, leaves exactly one row with `is_from_me = 1`; verify with `go test ./...`

## 3. Wire storage into the send path

- [x] 3.1 Change `sendWhatsAppMessage` to `sendWhatsAppMessage(client, messageStore *MessageStore, recipient, message, mediaPath string, mentions []string, logger waLog.Logger)`, keep the `resp` from `client.SendMessage`, and only after `err == nil` resolve `chatJID := resolveLID(client, recipientJID)`, `name := GetChatName(client, messageStore, chatJID, chatJID.String(), nil, chatJID.User, logger)`, `sender := client.Store.ID.User` (empty if `client.Store.ID` is nil) and the timestamp (`resp.Timestamp`, or `time.Now()` if zero), then call `storeSentMessage`; verify by reading the diff that every earlier `return false, ...` path precedes any store call
- [x] 3.2 On a `storeSentMessage` error, log `logger.Warnf("Failed to store sent message: %v", err)` and still return `true` with the existing success text; on success print the same `[time] → sender: ...` console line format `handleMessage` uses; verify `go build ./...` and that the `/api/send` response body and status codes are unchanged
- [x] 3.3 Change `startRESTServer` to accept `logger waLog.Logger`, pass `messageStore` and `logger` to `sendWhatsAppMessage` in the `/api/send` handler, and update the call in `main`; verify with `go build ./...` and `go vet ./...`

## 4. End-to-end verification

- [x] 4.1 With the bridge running and linked, send a plain text message via the MCP `send_message` tool and confirm `list_messages` for that chat immediately shows it as `From: Me` and `list_chats` lists the chat first with that message as last message
- [x] 4.2 Send a group message with `mentions` and an image with a caption via `send_file`; confirm both appear in `list_messages`, the image shows its caption and `download_media` retrieves the sent image
- [x] 4.3 Send to an invalid recipient (or a refused `media_path` containing `..`) and confirm the response is a failure and `SELECT COUNT(*) FROM messages` in `store/messages.db` is unchanged
- [x] 4.4 Send a message, then open the same chat on the phone and wait for any echo or restart the bridge to trigger history sync; confirm `SELECT COUNT(*) FROM messages WHERE id = '<sent id>'` returns 1

## 5. Documentation and release notes

- [x] 5.1 Check `README.md` for any statement that sent messages are not stored and update it if present; verify with `grep -n -i "sent messages\|not stored" README.md`
- [x] 5.2 Before committing, add a bullet to `CHANGELOG.md` in English (UK) under a `## [YYYY-MM-DD] - <Title>` heading dated in Mexico City CST (reusing today's heading if it already exists), e.g. `- fix: store messages sent through /api/send (text, mentions and media) as the user's own messages and update the chat's last message time, so list_messages and list_chats show them immediately (approach from daymade/whatsapp-mcp and upstream PRs #229 and #265)`; verify the entry sits at the top in reverse chronological order
- [x] 5.3 Run `go test ./...` in `whatsapp-bridge/` one final time and confirm all tests (existing caption, media URL and media filename tests plus the new ones) pass before the commit
