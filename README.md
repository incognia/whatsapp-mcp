# WhatsApp MCP Server

This is a Model Context Protocol (MCP) server for WhatsApp.

With this you can search and read your personal Whatsapp messages (including images, videos, documents, and audio messages), search your contacts and send messages to either individuals or groups. You can also send media files including images, videos, documents, and audio messages.

It connects to your **personal WhatsApp account** directly via the Whatsapp web multidevice API (using the [whatsmeow](https://github.com/tulir/whatsmeow) library). All your messages are stored locally in a SQLite database and only sent to an LLM (such as Claude) when the agent accesses them through tools (which you control).

Here's an example of what you can do when it's connected to Claude.

![WhatsApp MCP](./example-use.png)

> To get updates on this and other projects I work on [enter your email here](https://docs.google.com/forms/d/1rTF9wMBTN0vPfzWuQa2BjfGKdKIpTbyeKxhPMcEzgyI/preview)

> *Caution:* as with many MCP servers, the WhatsApp MCP is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). This means that project injection could lead to private data exfiltration.

## Installation

### Prerequisites

- Go
- Python 3.6+
- Anthropic Claude Desktop app (or Cursor)
- UV (Python package manager), install with `curl -LsSf https://astral.sh/uv/install.sh | sh`
- FFmpeg (_optional_) - Only needed for audio messages. If you want to send audio files as playable WhatsApp voice messages, they must be in `.ogg` Opus format. With FFmpeg installed, the MCP server will automatically convert non-Opus audio files. Without FFmpeg, you can still send raw audio files using the `send_file` tool.

### Steps

1. **Clone this repository**

   ```bash
   git clone https://github.com/lharries/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Run the WhatsApp bridge**

   Navigate to the whatsapp-bridge directory and run the Go application:

   ```bash
   cd whatsapp-bridge
   go run main.go
   ```

   The first time you run it, you will be prompted to scan a QR code. Scan the QR code with your WhatsApp mobile app to authenticate.

   After approximately 20 days, you will might need to re-authenticate.

3. **Connect to the MCP server**

   Copy the below json with the appropriate {{PATH}} values:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "{{PATH_TO_UV}}", // Run `which uv` and place the output here
         "args": [
           "--directory",
           "{{PATH_TO_SRC}}/whatsapp-mcp/whatsapp-mcp-server", // cd into the repo, run `pwd` and enter the output here + "/whatsapp-mcp-server"
           "run",
           "main.py"
         ]
       }
     }
   }
   ```

   For **Claude**, save this as `claude_desktop_config.json` in your Claude Desktop configuration directory at:

   ```
   ~/Library/Application Support/Claude/claude_desktop_config.json
   ```

   For **Cursor**, save this as `mcp.json` in your Cursor configuration directory at:

   ```
   ~/.cursor/mcp.json
   ```

4. **Restart Claude Desktop / Cursor**

   Open Claude Desktop and you should now see WhatsApp as an available integration.

   Or restart Cursor.

### Windows Compatibility

If you're running this project on Windows, be aware that `go-sqlite3` requires **CGO to be enabled** in order to compile and work properly. By default, **CGO is disabled on Windows**, so you need to explicitly enable it and have a C compiler installed.

#### Steps to get it working:

1. **Install a C compiler**  
   We recommend using [MSYS2](https://www.msys2.org/) to install a C compiler for Windows. After installing MSYS2, make sure to add the `ucrt64\bin` folder to your `PATH`.  
   → A step-by-step guide is available [here](https://code.visualstudio.com/docs/cpp/config-mingw).

2. **Enable CGO and run the app**

   ```bash
   cd whatsapp-bridge
   go env -w CGO_ENABLED=1
   go run main.go
   ```

Without this setup, you'll likely run into errors like:

> `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`

## Architecture Overview

This application consists of two main components:

1. **Go WhatsApp Bridge** (`whatsapp-bridge/`): A Go application that connects to WhatsApp's web API, handles authentication via QR code, and stores message history in SQLite. It serves as the bridge between WhatsApp and the MCP server.

2. **Python MCP Server** (`whatsapp-mcp-server/`): A Python server implementing the Model Context Protocol (MCP), which provides standardized tools for Claude to interact with WhatsApp data and send/receive messages.

### Data Storage

- All message history is stored in a SQLite database within the `whatsapp-bridge/store/` directory
- The database maintains tables for chats and messages
- Messages are indexed for efficient searching and retrieval

#### Loading older history

WhatsApp only sends message history once, when the device is paired. To load older messages for one chat later, without unlinking and re-pairing, the bridge can ask your phone for them:

- `POST /api/history/backfill` with `{"chat_jid": "<jid>", "count": 50}` requests up to `count` messages older than the oldest message already stored for that chat (the anchor). It answers `202 Accepted` immediately.
- Delivery is asynchronous: your phone must be online, and it answers within seconds with an on-demand history sync, which the bridge stores like any other history.
- `GET /api/history/backfill?chat_jid=<jid>` returns the status of the latest request for that chat: `pending`, `completed` (with `messages_stored` and `more_available`) or `timed_out` after 120 seconds. Status is kept in memory only.
- `count` must be between 1 and 200 (default 50). Requests are limited to one per chat every 30 seconds (never while one is pending) and one overall every 5 seconds; refusals return `429` with `Retry-After`.
- A chat with no stored messages cannot be backfilled, because there is no anchor (`404 no_anchor`). Wait for one new message in that chat first.
- Repeat the request to go further back: each one starts from the new oldest stored message.

Use it sparingly: whatsmeow is an unofficial client, and unusual traffic can put your account at risk. From Claude, use the `request_chat_history` tool.

### Console output

By default the bridge never prints message text, captions, media filenames or local file paths. Each live or sent message logs one metadata line (time, direction, chat, sender, media type and length), and each history sync logs one summary line per chat:

```text
[2026-10-06 23:40:28] ← 120363000000000000@g.us 5215500000001: text (17 chars)
History sync for 5215500000002@s.whatsapp.net: stored 19 messages (oldest 2025-11-01 08:00:00, newest 2026-10-06 21:14:03)
```

To see message content while debugging locally, start the bridge with `WHATSAPP_LOG_CONTENT=true` (or `1`); any other value keeps it off, and the bridge states the setting at start-up. It only adds content to these lines: whatsmeow's own log level does not change.

## Usage

Once connected, you can interact with your WhatsApp contacts through Claude, leveraging Claude's AI capabilities in your WhatsApp conversations.

### MCP Tools

Claude can access the following tools to interact with WhatsApp:

- **search_contacts**: Search for contacts by name or phone number
- **list_messages**: Retrieve messages with optional filters and context
- **list_chats**: List available chats with metadata
- **get_chat**: Get information about a specific chat
- **get_direct_chat_by_contact**: Find a direct chat with a specific contact
- **get_contact_chats**: List all chats involving a specific contact
- **get_last_interaction**: Get the most recent message with a contact
- **get_message_context**: Retrieve context around a specific message
- **send_message**: Send a WhatsApp message to a specified phone number or group JID
- **send_file**: Send a file (image, video, raw audio, document) to a specified recipient
- **send_audio_message**: Send an audio file as a WhatsApp voice message (requires the file to be an .ogg opus file or ffmpeg must be installed)
- **download_media**: Download media from a WhatsApp message and get the local file path
- **request_chat_history**: Ask your phone for older messages of one chat (see "Loading older history"); waits up to `wait_seconds` for the answer
- **create_listener**, **list_listeners**, **delete_listener**, **set_listener_enabled**, **test_listener**: Manage message listeners that post matching live messages to a webhook (see "Message listeners and webhooks")

### Message listeners and webhooks

A listener watches live messages as they arrive and, when one matches, sends it as a signed JSON `POST` to a webhook: a local n8n or Home Assistant, a script, or a push service. Listeners only read and notify; they never reply or take any action on your account.

**Criteria** (set at least one): `chat_jids`, `senders`, `contains` (case-insensitive, captions included), `regex` (RE2) and `mentions_me` (messages that tag you). With `match_mode: "or"` (default) any set criterion fires the listener; with `"and"` all of them must match. Several values in one list are always alternatives. Your own messages are ignored unless `include_from_me` is true. History sync, edits, status updates and messages older than `WEBHOOK_MAX_AGE` never fire a listener.

```sh
# Create: notify a local script when Amelia mentions "guardia" in the DevSecOps group
curl -s -X POST http://127.0.0.1:8080/api/listeners -H 'Content-Type: application/json' -d '{
  "name": "Guardias", "match_mode": "and",
  "chat_jids": ["120363422597955321@g.us"], "contains": ["guardia"],
  "webhook_url": "http://127.0.0.1:5678/webhook/wa", "secret": "a-secret-of-16-or-more-characters"
}'
curl -s http://127.0.0.1:8080/api/listeners                       # list (secrets never shown)
curl -s -X POST http://127.0.0.1:8080/api/listeners/1/test        # one signed test delivery
curl -s -X PATCH http://127.0.0.1:8080/api/listeners/1 -H 'Content-Type: application/json' -d '{"enabled": false}'
curl -s http://127.0.0.1:8080/api/listeners/1/deliveries          # recent delivery outcomes
curl -s -X DELETE http://127.0.0.1:8080/api/listeners/1
```

`POST /api/listeners/validate` checks a listener without saving it; invalid listeners get `400` with every problem in `errors`. From Claude, use the `create_listener`, `list_listeners`, `set_listener_enabled`, `test_listener` and `delete_listener` tools.

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
- The listener endpoints refuse requests with an `Origin` header, non-JSON bodies and unexpected `Host` headers, so a web page cannot create listeners. If you bind the API beyond loopback (`BIND_ADDR`), set `WEBHOOK_ADMIN_TOKEN` and send it as `Authorization: Bearer <token>`; without it, listener management is refused.
- Secrets are write-only, URL query values are masked in answers, logs show only the scheme and host, and the delivery log never stores message content.

| Variable | Default | Meaning |
|---|---|---|
| `WEBHOOK_ALLOWED_HOSTS` | (any) | Comma-separated host names, `*.domain` suffixes or IPs allowed as targets |
| `WEBHOOK_ADMIN_TOKEN` | (none) | Bearer token for the listener endpoints; required when `BIND_ADDR` is not loopback |
| `WEBHOOK_QUEUE_SIZE` | `256` | Deliveries waiting at most |
| `WEBHOOK_WORKERS` | `2` | Concurrent deliveries |
| `WEBHOOK_TIMEOUT` | `10s` | Per-attempt timeout (seconds or a Go duration) |
| `WEBHOOK_MAX_AGE` | `15m` | Ignore messages older than this (`0` disables) |

### Media Handling Features

The MCP server supports both sending and receiving various media types:

#### Media Sending

You can send various media types to your WhatsApp contacts:

- **Images, Videos, Documents**: Use the `send_file` tool to share any supported media type.
- **Voice Messages**: Use the `send_audio_message` tool to send audio files as playable WhatsApp voice messages.
  - For optimal compatibility, audio files should be in `.ogg` Opus format.
  - With FFmpeg installed, the system will automatically convert other audio formats (MP3, WAV, etc.) to the required format.
  - Without FFmpeg, you can still send raw audio files using the `send_file` tool, but they won't appear as playable voice messages.

#### Media Downloading

By default, just the metadata of the media is stored in the local database. The message will indicate that media was sent. To access this media you need to use the download_media tool which takes the `message_id` and `chat_jid` (which are shown when printing messages containing the meda), this downloads the media and then returns the file path which can be then opened or passed to another tool.

## Technical Details

1. Claude sends requests to the Python MCP server
2. The MCP server queries the Go bridge for WhatsApp data or directly to the SQLite database
3. The Go accesses the WhatsApp API and keeps the SQLite database up to date
4. Data flows back through the chain to Claude
5. When sending messages, the request flows from Claude through the MCP server to the Go bridge and to WhatsApp

## Troubleshooting

- If you encounter permission issues when running uv, you may need to add it to your PATH or use the full path to the executable.
- Make sure both the Go application and the Python server are running for the integration to work properly.

### Authentication Issues

- **QR Code Not Displaying**: If the QR code doesn't appear, try restarting the authentication script. If issues persist, check if your terminal supports displaying QR codes.
- **WhatsApp Already Logged In**: If your session is already active, the Go bridge will automatically reconnect without showing a QR code.
- **Device Limit Reached**: WhatsApp limits the number of linked devices. If you reach this limit, you'll need to remove an existing device from WhatsApp on your phone (Settings > Linked Devices).
- **No Messages Loading**: After initial authentication, it can take several minutes for your message history to load, especially if you have many chats.
- **WhatsApp Out of Sync**: If your WhatsApp messages get out of sync with the bridge, delete both database files (`whatsapp-bridge/store/messages.db` and `whatsapp-bridge/store/whatsapp.db`) and restart the bridge to re-authenticate.

For additional Claude Desktop integration troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues). The documentation includes helpful tips for checking logs and resolving common issues.
