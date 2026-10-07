## 1. Setting and helper

- [x] 1.1 Read `WHATSAPP_LOG_CONTENT` once at start-up into a package-level `logContent` (true only for `true`/`1`, case-insensitive) and print `Message content logging: on|off` at start-up; verify with a Go test of the parser (`true`, `1`, `TRUE`, `yes-please`, empty) and by starting the bridge and seeing the line
- [x] 1.2 Add `formatMessageLog(ts, outgoing, chatJID, sender, mediaType, filename, content)` per design D2; verify with table tests in `whatsapp-bridge/log_format_test.go` that the default form never contains the text, caption or filename and shows the rune count, and that the content form includes them

## 2. Live and sent messages

- [x] 2.1 Replace the two `fmt.Printf` calls in `handleMessage` with `formatMessageLog`; verify with `go vet ./...` and by reading the diff
- [x] 2.2 Replace the two `fmt.Printf` calls in `sendWhatsAppMessage` with `formatMessageLog` (outgoing); verify the same way
- [x] 2.3 Change the `/api/send` request and result lines per design D4 (no text unless `logContent`, never the media path); verify by reading the diff

## 3. History sync

- [x] 3.1 Remove the per-message `Message content:` line from `storeHistoryConversation`, make `Stored message:` conditional on `logContent`, and track the oldest stored time in `historyConversationResult`; verify the existing history and backfill tests still pass
- [x] 3.2 Log one summary line per conversation with stored messages in `processHistorySync`; verify with a test that captures the logger output for a synthetic two-message conversation and asserts one summary line with "stored 2 messages" and no message text

## 4. Verification and docs

- [x] 4.1 Run `go vet ./...` and `go test ./...` (with `-race`) in `whatsapp-bridge/`; verify both pass
- [x] 4.2 Restart the bridge without the setting, receive a live message and run an on-demand backfill; verify the console shows metadata and summary lines but no message text; then restart with `WHATSAPP_LOG_CONTENT=true` and verify the text appears
- [x] 4.3 Document `WHATSAPP_LOG_CONTENT` in the README (default off, what it shows, that it does not change whatsmeow's log level)
- [x] 4.4 Before committing, add `CHANGELOG.md` bullets in UK English under the current CST date heading (a `fix:` for keeping message content out of the console by default and a `feat:` for the opt-in); verify the entry sits at the top of that date block
