## Context

See proposal.md (Why) for the symptoms and specs/chat-last-message/spec.md for the contract.

Current state:

- `whatsapp-mcp-server/whatsapp.py`: `list_chats` (line ~493), `get_chat` and `get_direct_chat_by_contact` attach the last message with `LEFT JOIN messages ON chat_jid = jid AND chats.last_message_time = messages.timestamp`. `get_contact_chats` uses a different join (chats where a contact sent any message) and is out of scope.
- `whatsapp-bridge/main.go` `handleMessage` calls `StoreChat(chatJID, name, msg.Info.Timestamp)` (INSERT OR REPLACE, which also rewrites the time) before it extracts content and returns early for messages with neither text nor media. Reactions, protocol messages (revokes, ephemeral settings), polls and similar therefore move `last_message_time` without storing a row. `storeSentMessage` (sends) and `handleHistorySync` set the time from stored messages and are correct.
- `messages` has no index on `(chat_jid, timestamp)`; only the primary-key autoindex `(id, chat_jid)`. The store holds about 20,000 messages in 610 chats. SQLite 3.53 (window functions available since 3.25).
- Measured on the real store: the correlated subquery from upstream PR #283 (`messages.rowid = (SELECT rowid … WHERE chat_jid = chats.jid ORDER BY timestamp DESC LIMIT 1)`) takes ~667 ms per `list_chats` call; a `ROW_NUMBER()` window over `messages` takes ~15 ms; the current equality join ~8 ms (but wrong).

## Goals / Non-Goals

**Goals:**
- One shared way to pick a chat's last message in the MCP server, fast without schema changes.
- Stop the bridge from creating drift, and repair existing drift once per start.

**Non-Goals:**
- Adding indexes or any schema change (upstream PR #279 covers indexes; it can follow separately).
- Changing `get_contact_chats` semantics, storing reactions or other non-text events, or changing what counts as a message.
- Changing the MCP tool signatures or result fields.

## Decisions

### D1. Last message via a `ROW_NUMBER()` window, not a correlated subquery
The MCP server builds the last message per chat in one pass:

```sql
WITH last_messages AS (
  SELECT chat_jid, content, sender, is_from_me,
         ROW_NUMBER() OVER (PARTITION BY chat_jid ORDER BY timestamp DESC, rowid DESC) AS rn
  FROM messages
)
… LEFT JOIN last_messages lm ON lm.chat_jid = chats.jid AND lm.rn = 1
```

`rn = 1` guarantees at most one row per chat, so duplicates disappear by construction; `rowid DESC` breaks same-second ties deterministically (latest inserted wins). For `get_chat` and `get_direct_chat_by_contact`, which target one chat, the CTE is filtered by that chat's JID first so it stays cheap.

A small private helper in `whatsapp.py` returns the CTE and join fragments so the three readers share one definition.

*Alternatives:* the correlated subquery of PR #283 — correct but ~45× slower here without an index (it scans `messages` once per chat); `GROUP BY chat_jid` + `MAX(timestamp)` then joining back on timestamp — reintroduces duplicates for same-second ties; adding an index from the MCP server — rejected, the server must not write to the bridge's database.

### D2. Bridge: ensure the chat first, advance its time only when storing
Add `MessageStore.EnsureChat(jid, name string) error` (`INSERT OR IGNORE` with a NULL time, or keep the existing time) and change `handleMessage` to:
1. resolve chat, sender and name as today;
2. extract content and media;
3. if there is neither, call `EnsureChat` and return (new chats still appear, with their name);
4. otherwise call `StoreChat(chatJID, name, msg.Info.Timestamp)` and `StoreMessage` as today.

`StoreChat` stays INSERT OR REPLACE so stored messages keep refreshing the chat name, as today.

*Alternative:* keep updating the time for every event and rely only on the reader fix — rejected: `last_active` ordering would still be wrong (chats jump up on reactions) and the spec requires time and last message to agree.

### D3. Startup repair next to the existing migrations
Add `repairChatLastMessageTimes(messageStore, logger)` called after `migrateMediaFilenames`:

```sql
UPDATE chats SET last_message_time = (SELECT MAX(timestamp) FROM messages WHERE chat_jid = chats.jid)
WHERE EXISTS (SELECT 1 FROM messages WHERE chat_jid = chats.jid)
  AND last_message_time IS NOT (SELECT MAX(timestamp) FROM messages WHERE chat_jid = chats.jid)
```

It logs how many chats were repaired. Timestamps are stored as the same text format by go-sqlite3, so `MAX` and equality compare consistently for values written by the bridge.

*Alternative:* repair in the MCP server on read — rejected, the server is read-only towards the bridge's data.

## Risks / Trade-offs

- [Window over all messages grows with the store] → ~15 ms at 20k messages; linear, and an index on `(chat_jid, timestamp)` (PR #279) can be added later without changing behaviour.
- [Timestamps with different offsets compare as text] → all rows are written by this bridge with the machine's local offset; the repair uses the same values it compares, and the reader no longer depends on equality.
- [A chat created by a content-less first event has a NULL time and sorts last] → acceptable; it has no stored messages to show. It moves up as soon as a message is stored.
- [Reverting the bridge part re-introduces drift] → the reader fix alone already shows correct last messages; drift only affects sort order.

## Migration Plan

Rebuild and restart the bridge (the repair runs once at startup and is idempotent); restart the MCP server (Claude Code) for the reader change. No schema change. Rollback: revert the commit; repaired times remain valid.
