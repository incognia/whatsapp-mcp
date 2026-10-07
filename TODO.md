# TODO

Work worth doing in this fork, triaged from the 93 open issues of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp/issues) on 2026-10-07. Each item links the upstream issue it comes from. Behaviour changes go through OpenSpec (`/opsx:propose`) as usual.

## 1. Bugs

- [ ] **Documents sent with `send_file` arrive as unnamed `.bin` files** ([#327](https://github.com/lharries/whatsapp-mcp/issues/327)). The bridge sends `application/octet-stream` for anything that is not an image, audio or video, and sets `Title` but not `FileName` on the `DocumentMessage`. Derive the MIME type from the extension (`mime.TypeByExtension`) and set `FileName`.
- [ ] **Timestamps lose their time zone in MCP output** ([#146](https://github.com/lharries/whatsapp-mcp/issues/146)). `messages.db` stores an offset, but `format_message` prints `%Y-%m-%d %H:%M:%S` without it. Return ISO 8601 with the offset, or state the zone once per result.
- [ ] **Interactive and button messages break `list_messages`** ([#45](https://github.com/lharries/whatsapp-mcp/issues/45)). Reproduce with a WhatsApp Business bot; store their text (body, buttons, list title) like other content.
- [ ] **Chat names flip between a name and a phone number after a fresh link** ([#48](https://github.com/lharries/whatsapp-mcp/issues/48)). Probably reduced by the address-book lookup and LID merge; verify on a fresh link and refresh stored names from whatsmeow's contact store when a better one appears.
- [ ] **Images sometimes arrive blank** ([#78](https://github.com/lharries/whatsapp-mcp/issues/78)). Reproduce; the bridge sends no thumbnail, width or height for images, which WhatsApp clients may need for the preview.
- [ ] **Verify group sends** ([#62](https://github.com/lharries/whatsapp-mcp/issues/62), [#58](https://github.com/lharries/whatsapp-mcp/issues/58)): "failed to get device list" with `lid` servers or usync timeouts. Likely fixed by the whatsmeow upgrade (group sends work in daily use); confirm and comment upstream.

## 2. Features

- [ ] **Reply to a specific message** ([#121](https://github.com/lharries/whatsapp-mcp/issues/121)): optional `reply_to` (message ID) on `send_message`, sent as a quoted `ContextInfo`; also show "replying to" in `list_messages`.
- [ ] **Reactions** ([#147](https://github.com/lharries/whatsapp-mcp/issues/147), [#187](https://github.com/lharries/whatsapp-mcp/issues/187)): a `react_to_message` tool, and reactions shown on read (they are currently ignored, which is why they no longer move a chat's time). #187 also covers polls and events; consider those afterwards.
- [ ] **Sender names in results** ([#144](https://github.com/lharries/whatsapp-mcp/issues/144)): add `sender_name` to `list_messages` and `last_sender_name` to chats, resolved once per result (see the N+1 item below).
- [ ] **Group members** ([#66](https://github.com/lharries/whatsapp-mcp/issues/66)): a read-only `get_group_info` tool (name, topic, participants with names, admins) via `GetGroupInfo`.
- [ ] **Voice note transcription** ([#84](https://github.com/lharries/whatsapp-mcp/issues/84), [#289](https://github.com/lharries/whatsapp-mcp/issues/289), duplicate #290): opt-in, local (for example whisper.cpp), never sending audio to a third party by default; store the transcript alongside the message.
- [ ] **Delete a message for everyone, leave a group** ([#90](https://github.com/lharries/whatsapp-mcp/issues/90), [#73](https://github.com/lharries/whatsapp-mcp/issues/73)): destructive and outward-facing, so only behind `@writing_tool()`, the bridge guardrails and read-only mode; lower priority.
- [ ] **Configurable result limit for `search_contacts`** ([#37](https://github.com/lharries/whatsapp-mcp/issues/37)), currently a fixed constant.
- [ ] **Media metadata** ([#47](https://github.com/lharries/whatsapp-mcp/issues/47)): captions are already stored; consider size, duration and dimensions in `list_messages`.

## 3. Performance (MCP server)

- [ ] **SQLite indexes** ([#278](https://github.com/lharries/whatsapp-mcp/issues/278)) on `messages(chat_jid, timestamp)` and `messages(sender)`, through a versioned migration in `schema.go`; measure the last-message query again afterwards.
- [ ] **Full-text search** ([#280](https://github.com/lharries/whatsapp-mcp/issues/280)): an FTS5 table for `list_messages(query=…)`, ideally accent-insensitive like contact search.
- [ ] **N+1 sender lookups** ([#286](https://github.com/lharries/whatsapp-mcp/issues/286)): `format_message` opens a connection and runs a query per message for the sender name; resolve names once per result.
- [ ] **Reuse connections** ([#287](https://github.com/lharries/whatsapp-mcp/issues/287), [#288](https://github.com/lharries/whatsapp-mcp/issues/288)): one `requests.Session` for bridge calls and a per-thread SQLite connection.

## 4. Operations and tooling

- [ ] **Continuous integration** ([#149](https://github.com/lharries/whatsapp-mcp/issues/149)): GitHub Actions running `go vet`, `go test -race`, `uv run pytest` and `openspec validate --specs --strict` on pushes and pull requests.
- [ ] **Run the bridge in the background** ([#88](https://github.com/lharries/whatsapp-mcp/issues/88)): documented launchd and systemd units, and a clear log line or notification when the session needs re-linking.
- [ ] **Upgrade the `mcp` package** ([#83](https://github.com/lharries/whatsapp-mcp/issues/83)), pinned at 1.6.0; check FastMCP changes against the tool tests.
- [ ] **Desktop extension** ([#87](https://github.com/lharries/whatsapp-mcp/issues/87)): a `manifest.json` so Claude Desktop can install the MCP server in one step.
- [ ] **Linux installation notes** ([#53](https://github.com/lharries/whatsapp-mcp/issues/53)): packages needed on Ubuntu 24.04 (Go, a C compiler for CGO, uv, FFmpeg) in the README.
- [ ] **Shareable pairing link** ([#140](https://github.com/lharries/whatsapp-mcp/issues/140)): optionally print the pairing code as a URL besides the terminal QR code.

## Already handled in this fork

Commented upstream with the fixing commit: #9, #94, #97, #164, #198, #215, #220, #222, #225, #241, #266, #282, #344. Also solved here, not commented: the remaining 405 and `context.Context` duplicates (#109, #124, #136, #153, #154, #170, #192, #216, #237), sending policy (#218, covered by the guardrails) and the private security contact (#57, #129, now private vulnerability reporting).

## Not taken

Installation and support questions about the original setup (#18, #19, #20, #21, #23, #35, #36, #38, #39, #40, #41, #43, #44, #46, #63, #65, #70, #80, #85, #86), questions about the project's status (#126, #151, #263: could be answered with a pointer to this fork), marketplace and scanner promotions (#51, #204, #214, #246, #247, #309), issues about other software built on top (#238, #310), requests out of scope for this fork (#52, #64, #69, #123) and empty or unrelated posts (#108, #285, #297).
