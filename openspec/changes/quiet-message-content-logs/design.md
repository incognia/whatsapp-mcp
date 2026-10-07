## Context

See proposal.md (Why) and specs/bridge-logging/spec.md. Current content-bearing output in `whatsapp-bridge/main.go`:

| Where | Output |
|---|---|
| `handleMessage` | `fmt.Printf("[%s] %s %s: [%s: %s] %s\n", …)` / `"[%s] %s %s: %s\n"` — time, ←/→, sender, media type, filename, text |
| `sendWhatsAppMessage` (after `storeSentMessage`) | same two formats with → |
| `/api/send` handler | `fmt.Println("Received request to send message", req.Message, req.MediaPath)` and `fmt.Println("Message sent", success, message)` |
| `storeHistoryConversation` | `logger.Infof("Message content: %v, Media Type: %v", …)` for every message (stored or not) and `logger.Infof("Stored message: …")` for each stored one |

`logger` is `waLog.Stdout("Client", "INFO", true)`, the same logger passed to whatsmeow; `GetChatName` also logs chat names at INFO (not message content, out of scope).

## Goals / Non-Goals

**Goals:** no message text, captions, filenames or local paths in default output; one compact metadata line per live/sent message; one summary line per history conversation; an opt-in that restores today's detail.

**Non-Goals:** changing whatsmeow's own logging or level; removing chat names, phone numbers or JIDs from logs (metadata the operator needs); log files or rotation (the bridge only writes to stdout).

## Decisions

### D1. A dedicated opt-in, not the log level
`WHATSAPP_LOG_CONTENT` (`true`/`1` enables) is read once in `main` into a package-level `logContent bool`, printed at start-up next to the "Message listeners" line. Rejected: moving content to `Debugf` — the `Client` logger is shared with whatsmeow, so seeing content would require `DEBUG` for the whole client and flood the console with protocol traffic.

### D2. One formatting helper
`formatMessageLog(ts time.Time, outgoing bool, chatJID, sender, mediaType, filename, content string, withContent bool) string` (the setting is passed in, so tests do not touch the global) returns the line for both live and sent messages:
- default: `[2026-10-06 23:40:28] ← 120363000000000000@g.us 5215500000000: text (26 chars)` or `… image (12 chars caption)` — media type and character count only;
- with content: today's format plus the chat JID.
The chat JID is added in both modes because today's lines show only the sender, which is ambiguous in groups. Character count uses runes.

### D3. History sync: summary per conversation
`storeHistoryConversation` already returns `stored` and computes `latest`; it also tracks `oldest`. The per-message `Message content:` line is removed in both modes (it is noise even for debugging: it prints empty and skipped messages). The `Stored message:` line becomes conditional on `logContent`. `processHistorySync` logs one `History sync for <chat>: stored N messages (oldest … newest …)` line per conversation with N > 0, keeping the existing on-demand line.

### D4. `/api/send`
Request line: recipient, `media=true|false`, number of mentions; result line: success flag and the status message (which never contains the text). No text, no path, regardless of `logContent` for the path; the text is included only with `logContent`. A failed send's status message can embed the path (`Error reading media file: open /…`), so by default the result line keeps only the part before the first colon (`sendResultForLog`).

## Risks / Trade-offs

- [Operators lose a quick way to eyeball messages in the console] → `WHATSAPP_LOG_CONTENT=true`, documented in the README.
- [Third-party log readers parse the old format] → none known; the format was never documented.
- [Chat and group names still appear via `GetChatName` INFO lines] → names are metadata, out of scope; noted for a possible follow-up.

## Migration Plan

Rebuild and restart the bridge. Rollback: revert the commit, or set `WHATSAPP_LOG_CONTENT=true` to get the content back without reverting.
