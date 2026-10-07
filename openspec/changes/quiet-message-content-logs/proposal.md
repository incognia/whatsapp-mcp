## Why

The bridge prints the full text of every message to its console: incoming and outgoing live messages, messages sent through `/api/send` (with the local `media_path`), and every message of every history sync, twice (`Message content: …` and `Stored message: …`, including empty ones). That puts private conversations into terminal scrollback, terminal session logs, `nohup`/service logs and screenshots shared while debugging, which is exactly the kind of leak the webhook change was careful to avoid in its own logs. The behaviour comes from the original project and was never a deliberate choice.

## What Changes

- By default, the bridge logs message **metadata only**: time, direction (in/out), chat, sender, media type and the length of the text, never the text, captions, filenames or local file paths.
- History sync logs one summary line per conversation (chat, messages stored, oldest and newest time) instead of two lines per message, and drops the per-message `Message content:` line entirely.
- `/api/send` logs the recipient, whether a media file was attached and the outcome, but not the message text or the media path.
- A new opt-in setting, `WHATSAPP_LOG_CONTENT=true`, restores content in those lines for local debugging; the start-up output states whether it is on.
- The logger level of the shared whatsmeow `Client` logger is unchanged, so turning content on does not also turn on protocol debug output.
- README documents the setting and the default.

No change to stored data, the REST API or the MCP tools.

## Capabilities

### New Capabilities
- `bridge-logging`: what the bridge's console output may contain about messages, the content opt-in, and the shape of the default metadata lines.

### Modified Capabilities
<!-- None: webhook-delivery's "Logging hygiene" requirement already covers webhook logs and is unchanged. -->

## Impact

- **Code**: `whatsapp-bridge/main.go` — `handleMessage` (live log line), `sendWhatsAppMessage` (sent log line), the `/api/send` handler (request and result lines), `storeHistoryConversation`/`processHistorySync` (per-message lines replaced by a per-conversation summary); a small helper and the setting read at start-up (likely next to `webhook_config.go`'s settings). Go tests for the formatting helper.
- **Operations**: quieter console during history syncs; anyone who relied on reading message text in the bridge console sets `WHATSAPP_LOG_CONTENT=true`.
- **Docs**: README and CHANGELOG.
