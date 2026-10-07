## 1. MCP server: last message from the newest stored message

- [x] 1.1 Add a private helper in `whatsapp-mcp-server/whatsapp.py` that returns the `last_messages` CTE (`ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC, rowid DESC)`) and its `LEFT JOIN … rn = 1` fragment, optionally filtered to one chat JID, per design D1; verify with `uv run python -c "import whatsapp"`
- [x] 1.2 Use it in `list_chats` (replacing the timestamp-equality join when `include_last_message` is true, keeping the NULL columns path when false, the Python query filter and pagination unchanged); verify with new tests in `tests/test_last_message.py` for: drifted chat time still shows its newest message, two messages in the same second give one deterministic row, chat without messages is listed with empty last message, no duplicate chats, and the existing `tests/test_list_chats.py` still passing
- [x] 1.3 Use it in `get_chat` and `get_direct_chat_by_contact`; verify tests in `tests/test_last_message.py` that both report the newest stored message for a drifted chat and a same-second tie
- [x] 1.4 Extend `tests/conftest.py` only as needed (extra messages with drifted and tied timestamps in new test-local fixtures, not changing existing seeds); verify `uv run pytest` passes in full

## 2. Bridge: only stored messages advance a chat

- [x] 2.1 Add `MessageStore.EnsureChat(jid, name string) error` (`INSERT OR IGNORE`, leaving an existing row and its time untouched) in `whatsapp-bridge/main.go`; verify with a Go test that it creates a missing chat with its name and does not change an existing chat's name or time
- [x] 2.2 Reorder `handleMessage` per design D2: extract content and media first; when there is neither, call `EnsureChat` and return; otherwise `StoreChat` with the message time and `StoreMessage` as today; verify by reading the diff that no path calls `StoreChat` before deciding to store, and with `go vet ./...`
- [x] 2.3 Add `repairChatLastMessageTimes(messageStore, logger)` with the UPDATE from design D3, called on startup after `migrateMediaFilenames`, logging the number of repaired chats; verify with Go tests that a chat ahead of its newest message is moved back, a chat without messages keeps its time, names and messages are untouched, and a second run changes nothing

## 3. Verification and release notes

- [x] 3.1 Run `go test ./...` in `whatsapp-bridge/` and `uv run pytest` in `whatsapp-mcp-server/`; verify both pass
- [x] 3.2 Against a copy of the real `store/messages.db`, run the repair and compare before/after: chats with messages whose time differs from their newest message drop to 0, and `list_chats(limit=1000)` returns as many rows as distinct JIDs with `last_message` filled for every chat that has messages; also confirm `list_chats` stays under 50 ms
- [x] 3.3 Rebuild and restart the bridge and restart the MCP server; verify through the MCP tools that `list_chats` shows a last message for Eureka Underground and other active chats, lists no chat twice, and that a reaction received afterwards does not move its chat in `last_active` order
- [x] 3.4 Before committing, add `CHANGELOG.md` bullets in UK English under the current CST date heading (reusing it if it exists), e.g. `fix:` for the last-message lookup (crediting upstream PR #283 by HalemoGPA) and `fix:` for the bridge time handling and startup repair; verify the entry sits at the top of that date block
