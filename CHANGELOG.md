# Changelog

**Note:** All dates are in Mexico City CST (UTC-6).

<!-- markdownlint-disable MD013 MD024 MD022 MD032 -->
## [2026-10-07] - Project documentation, credits and agent guide

- test: replace real phone numbers, LIDs, group JIDs and contact names in the Go and Python test fixtures with fictitious ones, as `CONTRIBUTING.md` requires
- docs: use the same fictitious names in the main specs, the archived OpenSpec changes and a `list_chats` docstring example
- docs: archive the completed `prompt-injection-guardrails` OpenSpec change, add its two new main specs `openspec/specs/send-guardrails/spec.md` and `openspec/specs/untrusted-content/spec.md`, and replace the "Webhook URL safety policy" requirement in `openspec/specs/webhook-delivery/spec.md`, leaving no active changes
- fix: never send a file inside the bridge's own `store/` (session keys and message history), and refuse any `media_path` with a hidden component such as `~/.ssh`, `~/.aws` or `.env` unless a `WHATSAPP_MEDIA_ROOTS` entry deliberately includes that folder, answering `400` without reading the file, so a prompt-injected send cannot exfiltrate secrets
- feat: add `WHATSAPP_READ_ONLY` (`true` or `1`): the bridge refuses every send and every listener create, update and test delivery with `403`, while reads, history backfill, listing and deleting listeners keep working; the MCP server, given the same variable, no longer offers `send_message`, `send_file`, `send_audio_message`, `create_listener`, `set_listener_enabled` and `test_listener`
- feat: add the opt-in `WHATSAPP_SEND_RATE` (`<per minute>/<per hour>`) send limit, answering `429` with `Retry-After`, and the opt-in `WHATSAPP_SEND_ALLOWED` recipient allowlist (phone numbers or JIDs, LIDs resolved), answering `403`; sending is not limited by default, and the bridge reports these settings at start-up without listing recipients
- fix: BREAKING: without `WEBHOOK_ALLOWED_HOSTS`, listeners may only post to `localhost` and loopback or private-network IP addresses, at save time and at connection time, so a prompt-injected listener cannot post messages to the internet; host names and public services must be listed, and listeners saved under the older rule are kept but their deliveries fail with an error naming the setting, while they can still be disabled
- feat: declare in the MCP server's instructions that message text, captions, names, filenames and webhook results are third-party content never to be obeyed, and return each message's text between `<<message id=…>>` markers, with marker look-alikes inside the text neutralised and an untrusted-content notice at the start of text results, in `list_messages`, `get_message_context`, `get_last_interaction` and the message text of `list_chats`, `get_chat`, `get_direct_chat_by_contact` and `get_contact_chats`
- docs: add a Security section to the README (threat model, what the bridge enforces, optional limits, recommended setup and what is not covered), document the new settings and webhook rule, and add the guardrails to `AGENTS.md`
- docs: complete the `prompt-injection-guardrails` OpenSpec change, recording that host names need `WEBHOOK_ALLOWED_HOSTS` because they cannot be known to be local before they resolve, and the live checks of protected files, the rate limit and read-only mode
- docs: archive the completed `bridge-store-dir-under-go-run` OpenSpec change and sync its delta into the new main spec `openspec/specs/bridge-store-location/spec.md`, leaving `prompt-injection-guardrails` as the only active change
- fix: make `go run` use the bridge's real `store/`: under `go run` the bridge now uses its source folder instead of the folder of the executable, which `go run` builds in a temporary folder or, from Go 1.24, reuses from the Go build cache, so it no longer creates an empty store there, shows a new QR code or links a second device; `go run .` inside `whatsapp-bridge/` and `go -C whatsapp-bridge run .` from the repository root both work
- fix: stop start-up with a clear message recommending `go build` when the bridge runs under `go run` but its source folder cannot be trusted (for example with `-trimpath`), before any database is opened, and print the `store/` folder in use at every start-up
- docs: explain in the README that `go run` now works, add troubleshooting entries for the `Using store:` line and the new start-up error, and update the store location note in `AGENTS.md`
- docs: add and complete the `bridge-store-dir-under-go-run` OpenSpec change (proposal, new `bridge-store-location` delta spec, design and tasks), recording the cached `go run` executables found during the live check
- docs: add the `prompt-injection-guardrails` OpenSpec change (proposal, new `send-guardrails` and `untrusted-content` delta specs, a `webhook-delivery` delta limiting webhooks to private hosts unless allowed, design and tasks): files in the bridge's `store/` and hidden paths are never sent, third-party message text is marked as untrusted, and read-only mode, a send rate limit and a recipient allowlist are opt-in settings
- docs: rewrite the README for this fork: build the bridge with `go build` (the documented `go run main.go` no longer compiles, and `go run` would use a temporary `store/`), clone from `incognia/whatsapp-mcp`, require Go 1.26 and Python 3.11, add Claude Code setup, list the current tools, gather every setting in one table, describe storage, LIDs and start-up migrations, add development and troubleshooting notes, and replace real chat details in the listener example with fictitious ones
- docs: keep the MIT License and Luke Harries' copyright, and add copyright lines for Rodrigo Ernesto Álvarez Aguilera and the contributors listed in the new `AUTHORS.md`, which credits the original author, the authors of cherry-picked commits and the forks and pull requests whose approaches were adapted; add a licence footer to the README
- docs: add `CONTRIBUTING.md` covering privacy rules for test data, development setup, the OpenSpec workflow, code guidelines, Conventional Commits, CHANGELOG format, crediting ported work and the contribution licence
- docs: add `AGENTS.md` with guidance for AI coding agents (commands, hard rules on private data and sending, known pitfalls and conventions), and `CLAUDE.md` as a symlink to it
- chore: ignore the whole `.claude/` folder in Git and stop tracking the generated OpenSpec `/opsx` commands and skills, which contributors generate locally with `openspec init`

## [2026-10-06] - Mentions on send and a fixed `store/` path

- docs: archive the completed `quiet-message-content-logs` OpenSpec change and sync its delta into the new main spec `openspec/specs/bridge-logging/spec.md`, leaving no active changes
- fix: keep message content out of the bridge console by default: live and sent messages log one metadata line (time, direction, chat JID, sender, media type and character count) instead of their text, caption and filename; `/api/send` no longer logs the message text or `media_path`, and a failed send logs only its error category, since the details can carry the local path
- fix: replace the two per-message history sync lines (`Message content:`, printed even for empty and skipped messages, and `Stored message:`) with one summary line per chat giving the number of messages stored and their time range
- feat: add the `WHATSAPP_LOG_CONTENT` setting (`true` or `1`), which restores message text and filenames in those lines for local debugging without changing whatsmeow's log level, and report it at start-up
- docs: document the bridge's console output and `WHATSAPP_LOG_CONTENT` in the README
- docs: add the `quiet-message-content-logs` OpenSpec change (proposal, new `bridge-logging` delta spec, design and tasks)
- docs: archive the completed `message-webhooks` OpenSpec change and sync its two deltas into the new main specs `openspec/specs/message-listeners/spec.md` and `openspec/specs/webhook-delivery/spec.md`, leaving no active changes
- feat: add message listeners that watch live, stored messages by chat, sender, text (`contains`, captions included), RE2 `regex` and `mentions_me` (by phone number or LID), combined with an `or`/`and` match mode, ignoring the account's own messages unless `include_from_me` is set, and never firing for history sync, edits, status updates or messages older than `WEBHOOK_MAX_AGE` (model and match-mode naming from the AdamRussak fork, ADR 0001)
- feat: deliver each match as a JSON `POST` signed with HMAC-SHA256 over a timestamp and the body, through a bounded queue and fixed worker pool, with up to 4 attempts and timer-based backoff, no redirects and a delivery log that never stores message content
- feat: add the `/api/listeners` endpoints (create, list with last delivery, get, update, delete, validate, test delivery, delivery log) and the `create_listener`, `list_listeners`, `delete_listener`, `set_listener_enabled` and `test_listener` MCP tools; secrets are write-only and URL query values are masked
- feat: add the first versioned, additive schema migration to `messages.db` (`PRAGMA user_version`), creating the `listeners` and `listener_deliveries` tables
- fix: protect listener management from browser-forged requests (`Origin`, non-JSON bodies, foreign `Host`), require `WEBHOOK_ADMIN_TOKEN` when the API is not bound to loopback, and refuse webhook targets that are not `http(s)`, use plain `http` to public hosts, embed credentials, or resolve to link-local, metadata, multicast, broadcast or the bridge's own address
- docs: document message listeners and webhooks, the signature check and the `WEBHOOK_*` settings in the README
- docs: archive the completed `per-chat-history-sync` OpenSpec change and sync its delta into the new main spec `openspec/specs/history-backfill/spec.md`
- docs: record in the `per-chat-history-sync` design that the phone answers on-demand requests addressed by a one-to-one chat's LID, verified live on a personal chat and a group
- refactor: move the storage of each history sync conversation into `storeHistoryConversation`, with its client-dependent lookups injected, so history storage and backfill completion are tested without a live WhatsApp connection
- feat: add `POST /api/history/backfill` to the bridge, which asks the phone for up to 200 messages older than a chat's oldest stored message (an on-demand history sync, sent as a peer message) and answers `202` at once, plus `GET /api/history/backfill` to read the request's status (pending, completed with messages stored and whether more remain, or timed out); LID and phone-number chat JIDs and groups are accepted, and requests are rate-limited per chat and overall (approach from upstream PR #364 and the LukasHaas fork)
- feat: add the `request_chat_history` MCP tool, which requests older history for a chat and waits briefly for the phone's answer
- fix: never move a chat's last message time backwards or blank its name when an older history batch is stored, and move it only to the newest message actually stored from a history sync
- chore: remove the unused `requestHistorySync` function, which passed a nil anchor and sent the request to the wrong JID
- docs: document loading older history in the README
- docs: archive the completed `fix-chat-last-message` OpenSpec change and sync its delta into the new main spec `openspec/specs/chat-last-message/spec.md`
- docs: add and complete the `fix-chat-last-message` OpenSpec change (proposal, `chat-last-message` delta spec, design with the measured query trade-offs, and tasks)
- fix: report each chat's newest stored message as its last message in `list_chats`, `get_chat` and `get_direct_chat_by_contact`, using a window function instead of joining on an exact timestamp, so chats with messages no longer show an empty last message and no chat is listed twice; `get_chat` also stops failing when `include_last_message` is false (approach from upstream PR #283 by HalemoGPA, rewritten for speed without an index)
- fix: stop reactions and other events that are not stored from moving a chat's last message time in the bridge, and realign every chat's time with its newest stored message on startup
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
