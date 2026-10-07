# Changelog

**Note:** All dates are in Mexico City CST (UTC-6).

<!-- markdownlint-disable MD013 MD024 MD022 MD032 -->
## [2026-10-06] - Mentions on send and a fixed `store/` path

- docs: archive the completed `accent-insensitive-contact-search` OpenSpec change and sync its delta into the new main spec `openspec/specs/contact-search/spec.md`
- feat: make `search_contacts` search the phone's whole address book (saved, first, business and profile names, read strictly read-only from whatsmeow's store) as well as individual chats, so contacts without message history are found, with LID contacts reported once under their phone number (approach from the LukasHaas fork, upstream PR #343)
- feat: match contact searches ignoring accents and case, with every word required in any order and partial or formatted phone numbers accepted, ranking word-start matches and contacts with a chat first
- feat: apply the same matching to the `list_chats` query filter, including a chat's address-book names, and paginate after filtering
- fix: return chats from `list_chats` when `include_last_message` is false, which previously failed with a missing `messages` column and returned nothing
- chore: add a pytest suite for the MCP server, with fixtures that build temporary message and contact stores from fictitious data
- docs: archive the completed `store-sent-messages` OpenSpec change and sync its delta into the new main spec `openspec/specs/outgoing-message-storage/spec.md`, the project's first capability spec
- chore: ignore `.claude/settings.local.json` in Git, since it holds each user's local Claude Code permissions
- fix: store messages sent through `/api/send` (text, mentions and media with or without a caption) as the user's own messages and update the chat's last message time, so `list_messages` and `list_chats` show them immediately and `download_media` works on sent files; failed sends store nothing (approach from daymade/whatsapp-mcp and upstream PRs #229 and #265)
- docs: add OpenSpec change proposals, each with proposal, delta specs, design and tasks, for storing sent messages (`store-sent-messages`), accent-insensitive contact search over the full address book (`accent-insensitive-contact-search`), on-demand per-chat history backfill (`per-chat-history-sync`) and message listeners with signed webhook delivery (`message-webhooks`), based on the daymade, LukasHaas and AdamRussak forks and upstream PRs #229, #265, #343, #364, #326, #183 and #191
- chore: initialise OpenSpec with the spec-driven schema and UK English artefacts, and add its Claude Code `/opsx` commands and skills
- chore: ignore the bridge's `store/` folder in Git, so downloaded media and session data can never be committed by mistake
- fix: name generated media files after the message's own time plus a suffix of its ID instead of the time the bridge processed it, so media replayed during history sync no longer shares one filename and overwrites other files on download, and rename already stored media on startup (approach from upstream PR #270 by Smartinny)
- fix: store the captions of images, videos and documents as message content, unwrapping ephemeral, view-once and document-with-caption envelopes, and use the same text extraction for live and history-synced messages (upstream PR #350 by Matija Stepanic)
- fix: keep the signed query string when extracting the direct path from a media URL, so `download_media` no longer fails with a CDN 403 (upstream PR #361 by cherian)
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
