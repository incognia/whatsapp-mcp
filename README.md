# WhatsApp MCP Server

A Model Context Protocol (MCP) server for your **personal WhatsApp account**. With it, Claude (or another MCP client) can search and read your messages (including images, videos, documents and audio), search your contacts, send messages and files to people or groups, load older history for a chat and notify other programs when certain messages arrive.

It connects through the WhatsApp Web multi-device API using the [whatsmeow](https://github.com/tulir/whatsmeow) library. Your messages are stored locally in SQLite and only reach an LLM when the agent reads them through a tool call you control.

![WhatsApp MCP](./example-use.png)

> **Caution:** like many MCP servers, this one is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/): private data, untrusted content (anyone can message you) and the ability to send messages. A prompt injection in a message could lead to data exfiltration. Review what the agent sends, and keep tool approvals on for `send_*` tools.

> whatsmeow is an unofficial client. Unusual traffic can get an account restricted; use it for personal automation, not bulk messaging.

## About this fork

This is a maintained fork of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp). It brings the bridge up to date with current whatsmeow and LID addressing, and adds fixes and features collected from upstream pull requests and other forks:

- Messages sent from Claude are stored at once, with `@mentions` that notify in groups
- Contact search over the phone's whole address book, ignoring accents and case
- Correct last message per chat, and group senders and LID mentions resolved to phone numbers
- On-demand loading of older history for one chat
- Message listeners that post matching live messages to a signed webhook
- Safer defaults: API bound to loopback, media path restrictions, no message content in console logs

Every change is listed in [CHANGELOG.md](./CHANGELOG.md), and the behaviour of each capability is specified under [`openspec/specs/`](./openspec/specs/).

## Installation

### Prerequisites

- Go 1.26 or later
- Python 3.11 or later
- [uv](https://docs.astral.sh/uv/): `curl -LsSf https://astral.sh/uv/install.sh | sh`
- An MCP client: Claude Code, Claude Desktop or Cursor
- FFmpeg (_optional_): only needed to send audio that is not already `.ogg` Opus as a playable voice message

### 1. Clone the repository

```bash
git clone https://github.com/incognia/whatsapp-mcp.git
cd whatsapp-mcp
```

### 2. Build and run the bridge

```bash
cd whatsapp-bridge
go build -o whatsapp-bridge .
./whatsapp-bridge
```

The first time, the bridge shows a QR code: scan it from WhatsApp on your phone (**Settings › Linked devices › Link a device**). Your history then syncs for a few minutes. You may need to scan again after roughly 20 days without use.

Keep the bridge running while you use the MCP server. It always keeps its data in the `store/` folder next to the binary, whatever directory you start it from.

> Use `go build` rather than `go run`: `go run` places the binary in a temporary folder, so the bridge would create a new, empty `store/` there and ask for a new QR code.

**Windows:** `go-sqlite3` needs CGO, which is off by default. Install a C compiler (for example with [MSYS2](https://www.msys2.org/), adding its `ucrt64\bin` folder to `PATH`), then run `go env -w CGO_ENABLED=1` before building. Without it you will see `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`

### 3. Connect your MCP client

The MCP server is started by your client with uv; you do not run it yourself. Replace `/path/to/whatsapp-mcp` with the repository's absolute path (`pwd` inside the clone).

**Claude Code**

```bash
claude mcp add whatsapp --scope user -- uv --directory /path/to/whatsapp-mcp/whatsapp-mcp-server run main.py
```

Check it with `claude mcp get whatsapp`, then start a new Claude Code session.

**Claude Desktop or Cursor**

Add this to `~/Library/Application Support/Claude/claude_desktop_config.json` (Claude Desktop) or `~/.cursor/mcp.json` (Cursor), using the output of `which uv` as `command`, and restart the app:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "/path/to/uv",
      "args": ["--directory", "/path/to/whatsapp-mcp/whatsapp-mcp-server", "run", "main.py"]
    }
  }
}
```

## Usage

Ask in plain language: "summarise today's messages in the family group", "what did Ana say about Friday?", "send Luis the PDF in my Downloads folder".

### MCP tools

**Reading**

- **search_contacts**: find contacts by name or phone number across the whole address book (saved, first, business and profile names), ignoring accents and case; every word must appear, in any order, and partial or formatted numbers work
- **list_chats**: list chats, optionally filtered with the same matching and sorted by activity or name
- **get_chat**, **get_direct_chat_by_contact**, **get_contact_chats**: chat details, a contact's one-to-one chat, or every chat involving a contact
- **list_messages**: messages filtered by chat, sender, text and dates, with optional context; mentions are shown as `@Name`
- **get_message_context**, **get_last_interaction**: messages around one message, or the latest message with a contact
- **download_media**: download a message's image, video, document or audio and return its local path
- **request_chat_history**: ask your phone for older messages of one chat (see "Loading older history")

**Sending**

- **send_message**: send text to a phone number or group JID; `mentions` (phone numbers) tags people in a group, with `@<number>` in the text where each tag goes
- **send_file**: send an image, video, document or raw audio
- **send_audio_message**: send audio as a playable voice message (`.ogg` Opus, or any format with FFmpeg installed)

Sent messages are stored immediately as your own, so they show up in `list_messages` and `list_chats` straight away.

**Listeners**

- **create_listener**, **list_listeners**, **set_listener_enabled**, **test_listener**, **delete_listener**: manage message listeners (see "Message listeners and webhooks")

### Media

Only media metadata is stored. To get a file, call `download_media` with the `message_id` and `chat_jid` shown next to the media message; it returns the local path. Generated filenames use the message's time and ID, so files never overwrite each other.

To send media, the MCP server passes a local path to the bridge. Paths containing `..` are refused, and `WHATSAPP_MEDIA_ROOTS` can restrict sending to chosen folders.

### Loading older history

WhatsApp only sends message history once, when the device is linked. To load older messages for one chat later, without unlinking and linking again, the bridge can ask your phone for them. From Claude, use `request_chat_history`; the REST API is:

- `POST /api/history/backfill` with `{"chat_jid": "<jid>", "count": 50}` requests up to `count` messages older than the oldest message already stored for that chat (the anchor). It answers `202 Accepted` immediately.
- Delivery is asynchronous: your phone must be online, and it answers within seconds with an on-demand history sync, which the bridge stores like any other history.
- `GET /api/history/backfill?chat_jid=<jid>` returns the status of the latest request for that chat: `pending`, `completed` (with `messages_stored` and `more_available`) or `timed_out` after 120 seconds. Status is kept in memory only.
- `count` must be between 1 and 200 (default 50). Requests are limited to one per chat every 30 seconds (never while one is pending) and one overall every 5 seconds; refusals return `429` with `Retry-After`.
- A chat with no stored messages cannot be backfilled, because there is no anchor (`404 no_anchor`). Wait for one new message in that chat first.
- Repeat the request to go further back: each one starts from the new oldest stored message.

Use it sparingly.

### Message listeners and webhooks

A listener watches live messages as they arrive and, when one matches, sends it as a signed JSON `POST` to a webhook: a local n8n or Home Assistant, a script, or a push service. Listeners only read and notify; they never reply or take any action on your account.

**Criteria** (set at least one): `chat_jids`, `senders`, `contains` (case-insensitive, captions included), `regex` (RE2) and `mentions_me` (messages that tag you). With `match_mode: "or"` (default) any set criterion fires the listener; with `"and"` all of them must match. Several values in one list are always alternatives. Your own messages are ignored unless `include_from_me` is true. History sync, edits, status updates and messages older than `WEBHOOK_MAX_AGE` never fire a listener.

```sh
# Create: notify a local script when someone writes "deploy" in a team group
curl -s -X POST http://127.0.0.1:8080/api/listeners -H 'Content-Type: application/json' -d '{
  "name": "Deploys", "match_mode": "and",
  "chat_jids": ["120363000000000000@g.us"], "contains": ["deploy"],
  "webhook_url": "http://127.0.0.1:5678/webhook/wa", "secret": "a-secret-of-16-or-more-characters"
}'
curl -s http://127.0.0.1:8080/api/listeners                       # list (secrets never shown)
curl -s -X POST http://127.0.0.1:8080/api/listeners/1/test        # one signed test delivery
curl -s -X PATCH http://127.0.0.1:8080/api/listeners/1 -H 'Content-Type: application/json' -d '{"enabled": false}'
curl -s http://127.0.0.1:8080/api/listeners/1/deliveries          # recent delivery outcomes
curl -s -X DELETE http://127.0.0.1:8080/api/listeners/1
```

`POST /api/listeners/validate` checks a listener without saving it; invalid listeners get `400` with every problem in `errors`.

**Payload** (`X-Webhook-Event: message`, `version: 1`): `delivery_id`, `listener` (`id`, `name`), `match_mode`, `matched` (criteria that matched) and `message` with `id`, `chat_jid`, `chat_name`, `is_group`, `sender`, `sender_jid`, `sender_name`, `timestamp` (RFC 3339, UTC), `content` (at most 4,096 characters, with `content_truncated` when cut), `media_type`, `filename`, `is_from_me` and `mentions_me`. Media is not embedded; fetch it with `download_media`.

**Signature**: with a secret, each request carries `X-Webhook-Timestamp` and `X-Webhook-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>." + body>`. Verify it and reject old timestamps:

```python
import hashlib, hmac, time

def verify(secret: str, headers, body: bytes, tolerance: int = 300) -> bool:
    timestamp = headers["X-Webhook-Timestamp"]
    expected = "sha256=" + hmac.new(secret.encode(), timestamp.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, headers.get("X-Webhook-Signature", "")) and abs(time.time() - int(timestamp)) <= tolerance
```

**Delivery**: asynchronous, by a small pool of workers behind a bounded queue, so a slow receiver never delays WhatsApp. A `2xx` answer means delivered. Network errors, timeouts, `408`, `429` and `5xx` are retried up to 4 attempts (about 2, 10 and 30 seconds apart, honouring `Retry-After` up to 60 seconds); other `4xx` answers and redirects fail at once. A full queue drops the delivery and logs it. Deliveries are not guaranteed across restarts: de-duplicate on `message.id` or `X-Webhook-Delivery`.

**Safety**:

- `webhook_url` must be `http` or `https` with no embedded credentials; `https` is required except for `localhost` and private networks. Link-local and cloud metadata addresses (`169.254.169.254`), multicast, broadcast and the bridge's own API are refused, also after DNS resolution. `WEBHOOK_ALLOWED_HOSTS` restricts targets further.
- The listener endpoints refuse requests with an `Origin` header, non-JSON bodies and unexpected `Host` headers, so a web page cannot create listeners. If you bind the API beyond loopback (`BIND_ADDR`), set `WEBHOOK_ADMIN_TOKEN` and send it as `Authorization: Bearer <token>`; without it, listener management is refused. The MCP server sends it too when `WEBHOOK_ADMIN_TOKEN` is set in its environment.
- Secrets are write-only, URL query values are masked in answers, logs show only the scheme and host, and the delivery log never stores message content.

## Configuration

All settings are environment variables of the bridge (`WEBHOOK_ADMIN_TOKEN` also of the MCP server). Example: `BIND_ADDR=0.0.0.0 WEBHOOK_ADMIN_TOKEN=… ./whatsapp-bridge`.

| Variable | Default | Meaning |
|---|---|---|
| `BIND_ADDR` | `127.0.0.1` | Address of the REST API (port 8080). The API has no authentication and can read and send messages, so only change it on a network you trust |
| `WHATSAPP_MEDIA_ROOTS` | (any path without `..`) | Folders, separated by `:` (`;` on Windows), that media may be sent from |
| `WHATSAPP_LOG_CONTENT` | off | `true` or `1` prints message text and filenames in the console, for local debugging |
| `WEBHOOK_ALLOWED_HOSTS` | (any) | Comma-separated host names, `*.domain` suffixes or IPs allowed as webhook targets |
| `WEBHOOK_ADMIN_TOKEN` | (none) | Bearer token for the listener endpoints; required when `BIND_ADDR` is not loopback |
| `WEBHOOK_QUEUE_SIZE` | `256` | Deliveries waiting at most |
| `WEBHOOK_WORKERS` | `2` | Concurrent deliveries |
| `WEBHOOK_TIMEOUT` | `10s` | Per-attempt timeout (seconds or a Go duration) |
| `WEBHOOK_MAX_AGE` | `15m` | Ignore messages older than this (`0` disables) |

## Architecture

Two components:

1. **Go bridge** (`whatsapp-bridge/`): connects to WhatsApp, handles linking by QR code, stores chats and messages in SQLite and serves a REST API on `127.0.0.1:8080` for sending, media download, history backfill and listeners.
2. **Python MCP server** (`whatsapp-mcp-server/`): exposes the MCP tools. It reads the SQLite databases directly (read-only for whatsmeow's store) and calls the bridge's REST API for anything that talks to WhatsApp.

```text
Claude ⇄ MCP server ──reads──▶ store/messages.db, store/whatsapp.db
             │                         ▲
             └──REST (localhost)──▶ Go bridge ⇄ WhatsApp
```

### Data storage

- `whatsapp-bridge/store/messages.db`: chats, messages and listeners, written by the bridge
- `whatsapp-bridge/store/whatsapp.db`: whatsmeow's session, contacts and LID mappings
- WhatsApp's newer LID identifiers (`…@lid`) are translated to phone numbers for chats, senders and mentions, so one conversation is never split in two
- On start-up the bridge applies schema migrations and data repairs (merging LID chats, fixing group senders and mentions, renaming media files, realigning each chat's last message time). They are additive and safe to run on every start

`store/` is ignored by Git. It holds your whole message history and the session key: do not share it.

### Console output

By default the bridge never prints message text, captions, media filenames or local file paths. Each live or sent message logs one metadata line (time, direction, chat, sender, media type and length), and each history sync logs one summary line per chat:

```text
[2026-10-06 23:40:28] ← 120363000000000000@g.us 5215500000001: text (17 chars)
History sync for 5215500000002@s.whatsapp.net: stored 19 messages (oldest 2025-11-01 08:00:00, newest 2026-10-06 21:14:03)
```

To see message content while debugging locally, start the bridge with `WHATSAPP_LOG_CONTENT=true` (or `1`); any other value keeps it off, and the bridge states the setting at start-up. It only adds content to these lines: whatsmeow's own log level does not change.

## Development

```bash
cd whatsapp-bridge && go vet ./... && go test -race ./...
cd whatsapp-mcp-server && uv run pytest
```

The Python tests build temporary databases from fictitious data; neither suite needs a WhatsApp connection.

Changes follow [OpenSpec](https://github.com/Fission-AI/OpenSpec) (spec-driven schema, UK English): each change has a proposal, delta specs, design and tasks under `openspec/changes/`, and is archived into the main specs in `openspec/specs/` once done. Commits follow Conventional Commits, and every change is recorded in `CHANGELOG.md`.

See [CONTRIBUTING.md](./CONTRIBUTING.md) before opening a pull request.

## Troubleshooting

- **The MCP server shows no messages, or the bridge asks for a QR code again**: the bridge was probably started with `go run` or from an old binary elsewhere, so it used another `store/`. Build it with `go build -o whatsapp-bridge .` inside `whatsapp-bridge/` and run that binary.
- **"Client outdated (405)"**: WhatsApp rejects old whatsmeow versions. Update it with `go get go.mau.fi/whatsmeow@latest && go mod tidy`, rebuild and restart.
- **Sending fails with "connection refused"**: the bridge is not running, or is bound to another address.
- **QR code not displaying**: restart the bridge and make sure your terminal is wide enough to draw it.
- **Already linked**: with an active session, the bridge reconnects without a QR code.
- **Device limit reached**: remove a linked device in WhatsApp on your phone (**Settings › Linked devices**).
- **No messages after linking**: the first history sync can take several minutes with many chats.
- **A chat is missing older messages**: load them with `request_chat_history` instead of linking again.
- **Out of sync beyond repair**: stop the bridge, delete `whatsapp-bridge/store/messages.db` and `whatsapp-bridge/store/whatsapp.db`, and start it again to link from scratch. This loses listeners and any history your phone no longer sends.
- **uv permission or path errors**: use the full path from `which uv` in your MCP client's configuration.

For Claude Desktop integration issues, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues).

## Credits

Created by [Luke Harries](https://github.com/lharries) ([original repository](https://github.com/lharries/whatsapp-mcp); [updates on his projects](https://docs.google.com/forms/d/1rTF9wMBTN0vPfzWuQa2BjfGKdKIpTbyeKxhPMcEzgyI/preview)). This fork includes commits from upstream pull requests and approaches from the daymade, LukasHaas and AdamRussak forks. [AUTHORS.md](./AUTHORS.md) lists everyone whose work is included, and [CHANGELOG.md](./CHANGELOG.md) credits the source of each change.

---

*This fork is maintained by Rodrigo Álvarez (@incognia) and distributed under the MIT License. For details, see the LICENSE file.*

*Copyright © 2025 Luke Harries. Copyright © 2026 Rodrigo Ernesto Álvarez Aguilera and the contributors listed in AUTHORS.md.*
