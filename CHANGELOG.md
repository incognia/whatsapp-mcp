# Changelog

**Note:** All dates are in Mexico City CST (UTC-6).

<!-- markdownlint-disable MD013 MD024 MD022 MD032 -->
## [2026-10-06] - Mentions on send and a fixed `store/` path

- fix: refuse `media_path` values containing `..` in `/api/send` and, when `WHATSAPP_MEDIA_ROOTS` is set, only read media from those directories, closing a CWE-22 path traversal (upstream PR #275 by HalemoGPA)
- fix: bind the bridge REST API to `127.0.0.1` by default instead of every network interface, since it has no authentication and can read and send messages on the linked account; set `BIND_ADDR=0.0.0.0` to opt into LAN exposure (upstream PR #224 by jmmgreg)
- feat: accept an optional `mentions` list on `/api/send` and send it as an extended text message with `ContextInfo.MentionedJID`, so group tags notify the mentioned person
- feat: expose the `mentions` parameter on the MCP server's `send_message` tool
- fix: always use the `store/` folder next to the bridge binary, whatever directory it is launched from; previously, starting it from another path created a new session that the MCP server never read
- chore: ignore the compiled `whatsapp-bridge/whatsapp-bridge` binary in Git

## [2026-10-03] - Group senders and LID mentions

- fix: read the sender of group messages from `WebMessageInfo.participant` when the message key does not carry it, as recent history syncs do; previously the group itself was stored as the sender
- feat: translate "@<LID>" mentions in message text to "@<phone number>" when storing messages, and fix already stored ones on startup
- feat: show "@<name>" instead of "@<phone number>" when the MCP server formats messages, and "@Me" for the user's own mentions

## [2026-10-02] - Compatibility with current whatsmeow and LID addressing

- fix: upgrade `whatsmeow` to `v0.0.0-20260929112325` to resolve the "Client outdated (405)" connection error, passing `context.Context` to its updated API calls
- fix: translate LID JIDs (`@lid`) to their phone number for chats and senders, in both live messages and history sync, so a single conversation is no longer split into two chats
- feat: merge chats and senders already stored under LIDs into their phone-number equivalents on startup
