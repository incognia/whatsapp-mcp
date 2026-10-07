## Context

See proposal.md (Why). The risky paths today:

| Path | Today | Worst case under prompt injection |
|---|---|---|
| `send_message` | any recipient, no limit | history or summaries sent to the attacker's number; bulk sends get the account banned |
| `send_file` / `send_audio_message` | any readable path without `..` unless `WHATSAPP_MEDIA_ROOTS` is set (`validateMediaPath`) | `~/.ssh/id_*`, `.env`, or the bridge's own `store/whatsapp.db` (session keys: full account takeover) sent out |
| `create_listener` | any `https` host, or any host on `WEBHOOK_ALLOWED_HOSTS` when set | a standing channel posting every new message to the attacker |
| Read tools | message text returned inline | the attack vector itself: third-party text read as instructions |

Client-side approval (Claude Code asks before each MCP tool call unless the user allows it permanently) is the strongest control, but it is outside this repository and users often turn it off for convenience.

## Goals / Non-Goals

**Goals:** limits the model cannot bypass on the paths above, without slowing or blocking normal sending by default; an opt-in way to remove the send leg entirely; untrusted text clearly marked; safe defaults that do not break normal personal use (a few messages and files to people the user chooses).

**Non-Goals:** detecting prompt injection in message text (unreliable, easy to evade); per-send human confirmation inside the server (see D6); protecting against a malicious local user or process, which can already read `store/` directly; limiting what the model *reads* (summaries are the point of the tool).

## Decisions

### D1. Enforce in the bridge, mirror in the MCP server
Every send and listener change goes through the bridge's REST API, so read-only mode, protected files, the rate limit and the allowlist live there (`send_guardrails.go`), and hold even if someone calls the API directly or another MCP client is used. The MCP server additionally hides tools in read-only mode, so the model does not even see them. Read-only mode reads the same variable name in both processes; the README shows how to set it in the MCP client config and the bridge's environment.

### D2. Protected files
Always refuse paths under the bridge's `store/` (resolved with `EvalSymlinks`, compared with the bridge directory). Hidden components (`.ssh`, `.aws`, `.gnupg`, `.env`, `.git`, `.config`, …) are refused by a single rule — any component starting with `.` — rather than a list of known secret folders, which would always be incomplete. A `WHATSAPP_MEDIA_ROOTS` entry that itself includes the hidden component re-allows it (for example `~/.local/share/outbox`), so the rule is overridable but only deliberately. Rejected: making `WHATSAPP_MEDIA_ROOTS` mandatory, which breaks the common "send the PDF in Downloads" request for every existing user.

### D3. Rate limit is opt-in
Sending is a core use of this server, so no default limit: the user decided that writing must never be slowed unless they ask for it. When `WHATSAPP_SEND_RATE` is set, the limit is global (not per recipient: exfiltration targets one recipient), counted only for sends that pass all other checks, in memory with a sliding window, mirroring the backfill limiter. The README suggests `10/60` for users who want it.

### D4. Recipient allowlist is opt-in
A default allowlist ("only chats you have written to before") was considered and rejected: the attacker is usually someone who messaged the user, so their chat already exists and the default would give false comfort. `WHATSAPP_SEND_ALLOWED` is for users who only ever send to a few chats (themselves, a family group).

### D5. Webhooks: private by default
Public `https` targets need an explicit `WEBHOOK_ALLOWED_HOSTS` entry. This is the only breaking default: a listener is a persistent exfiltration channel that survives the session, so it deserves the strictest default. Without an allowlist only `localhost` and loopback or private IP literals are accepted, since a host name cannot be known to be local before it resolves; host names, local or not, go in `WEBHOOK_ALLOWED_HOSTS`. The connection-time check also refuses public addresses when no allowlist is set. Listener updates that keep the saved URL do not re-check it, so a listener from an older policy can still be disabled. Existing listeners are not deleted; their deliveries fail with a message naming the setting, visible in the delivery log and `list_listeners`.

### D6. Untrusted-content marking, and why not in-server confirmation
FastMCP's `instructions` carry the policy text; `format_message` wraps each message's text as `<<message id=… from=…>> … <</message id=…>>` and replaces any `<<`/`>>` sequences inside the text with look-alike characters, so a message cannot fake the end of its own block. This raises the bar but does not stop a determined injection; it is defence in depth. Per-send confirmation through MCP elicitation was considered: it is the right long-term control, but client support is uneven today and a silent fallback would be worse than none. It stays a follow-up once the user's clients support it.

## Risks / Trade-offs

- [Only the always-on guardrails protect a default setup] → by default only hidden files, the store and public webhooks are blocked; read-only mode, the rate limit and the allowlist are opt-in and explained in the README for users who want more.
- [Hidden-component rule blocks a legitimate file] → the error message names the rule and the `WHATSAPP_MEDIA_ROOTS` override.
- [Breaking public webhooks] → CHANGELOG `BREAKING` note, clear 400 and delivery errors, one-line fix (`WEBHOOK_ALLOWED_HOSTS`).
- [Marking leaks into summaries the model writes] → markers are short and the instructions tell the model not to quote them.
- [Model still exfiltrates through text it is allowed to send] → bounded only if the user sets the rate limit or the allowlist; ultimately covered only by client approval, which the docs recommend keeping on for send tools.

## Migration Plan

Rebuild and restart the bridge; restart the MCP client session. Users with public webhooks add their hosts to `WEBHOOK_ALLOWED_HOSTS`. Rollback: revert the commit; no data changes.
