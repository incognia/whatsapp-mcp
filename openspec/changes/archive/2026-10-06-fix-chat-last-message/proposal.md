## Why

The MCP tools often report a chat with no last message, or list the same chat twice, even though its messages are stored. On the current store, 17 chats that do have messages show `last_message: null` in `list_chats`, 11 chats carry a `last_message_time` later than their newest stored message, and `list_chats` returns 612 rows for 610 chats. Two defects combine: the bridge moves `chats.last_message_time` forward for events it then discards (reactions, protocol and other content-less messages), and the MCP server finds the last message by joining on exact equality `chats.last_message_time = messages.timestamp`, which finds nothing when the two drift and returns one row per message when two messages share a timestamp.

## What Changes

- The MCP server's `list_chats`, `get_chat` and `get_direct_chat_by_contact` find a chat's last message as its newest stored message (latest timestamp, ties broken deterministically), instead of joining on timestamp equality, so a chat with stored messages always shows one, and each chat appears exactly once.
- The bridge's `handleMessage` only advances a chat's `last_message_time` when the incoming message is actually stored; content-less events still ensure the chat row exists (name included) without moving its time.
- On startup, the bridge repairs drifted rows: a chat whose `last_message_time` differs from its newest stored message's timestamp is set to that timestamp; chats with no stored messages keep their current time.
- `last_message_time` keeps driving the `last_active` sort order and stays consistent with the reported last message.

## Capabilities

### New Capabilities
- `chat-last-message`: how a chat's last message and last activity time are determined, kept consistent between the bridge and the MCP server, and repaired when they drift.

### Modified Capabilities
<!-- None: outgoing-message-storage already requires sends to set last_message_time to the stored message's timestamp, which this change keeps; contact-search is unaffected. -->

## Impact

- **Code**: `whatsapp-mcp-server/whatsapp.py` (`list_chats`, `get_chat`, `get_direct_chat_by_contact` last-message lookup); `whatsapp-bridge/main.go` (`handleMessage` ordering, a new startup repair next to `migrateLIDChats`/`migrateMediaFilenames`, and a `MessageStore` helper to create a chat without touching its time). New tests in `whatsapp-mcp-server/tests/` and `whatsapp-bridge/`.
- **Data**: `chats.last_message_time` corrected once at startup for drifted rows; no schema change.
- **APIs**: MCP tool signatures and result fields unchanged; `list_chats` no longer returns duplicate chats and fills `last_message` for every chat with stored messages.
- **Credits**: the reader-side fix follows upstream PR lharries/whatsapp-mcp #283 (issue #282, by HalemoGPA), which replaces the timestamp-equality join with a subquery on the newest message.
