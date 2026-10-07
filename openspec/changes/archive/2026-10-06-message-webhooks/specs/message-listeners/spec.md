## Purpose

Lets the user define listeners that watch live WhatsApp messages arriving at the bridge — by chat, sender, text, regular expression or a mention of the user — and decide, reliably and on normalised phone-number identities, which messages should trigger a notification.

## ADDED Requirements

### Requirement: Listener definition
A listener SHALL consist of: a numeric `id` assigned by the bridge; a `name` (1–100 characters); `enabled` (default `true`); `match_mode`, either `"or"` or `"and"` (default `"or"`); the match criteria `chat_jids` (list), `senders` (list), `contains` (list of text fragments), `regex` (one pattern), and `mentions_me` (boolean, default `false`); `include_from_me` (boolean, default `false`); a delivery target `webhook_url`; and an optional `secret` used to sign deliveries. A criterion is *set* when its list is non-empty, its pattern is non-empty, or, for `mentions_me`, when it is `true`. Listeners SHALL be persisted in `store/messages.db` and SHALL survive a restart of the bridge.

#### Scenario: Defaults are applied
- **WHEN** a listener is created with only `name`, `webhook_url` and `contains: ["guardia"]`
- **THEN** the stored listener has `enabled: true`, `match_mode: "or"`, `mentions_me: false` and `include_from_me: false`

#### Scenario: Listener survives a restart
- **WHEN** a listener is created and the bridge is restarted
- **THEN** the listener is listed again with the same `id` and criteria and keeps matching new messages

### Requirement: Listener validation
The bridge SHALL reject a listener that is invalid, and SHALL report every problem found rather than only the first. A listener is invalid when: `name` is missing or longer than 100 characters; no criterion is set; `match_mode` is anything other than `"or"` or `"and"` (an unknown value MUST NOT be treated as `"or"`); `regex` does not compile or is longer than 500 characters; a `contains` entry is empty or longer than 200 characters, or there are more than 20 entries; `chat_jids` or `senders` holds more than 50 entries, or an entry that is neither a phone number nor a valid WhatsApp JID; `webhook_url` breaks the URL safety policy of the `webhook-delivery` capability; or creating it would exceed 50 listeners. The same validation SHALL apply to creation, to updates (on the listener as it would be after the update) and to the validate-only operation.

#### Scenario: Invalid listener rejected with every problem
- **WHEN** a client creates a listener with no name, `match_mode: "xor"` and `regex: "([a-z"`
- **THEN** the bridge answers `400` with `success: false`, an `error` string holding the first problem, and an `errors` array naming the `name`, `match_mode` and `regex` problems, and nothing is stored

#### Scenario: Listener without criteria rejected
- **WHEN** a client creates a listener whose `chat_jids`, `senders` and `contains` are empty, `regex` is empty and `mentions_me` is `false`
- **THEN** the bridge answers `400` explaining that at least one criterion is required

#### Scenario: Validate without saving
- **WHEN** a client sends a listener to the validate-only operation
- **THEN** the bridge answers with the same result a creation would give and stores nothing

### Requirement: Identifier normalisation
Chat and sender values SHALL be normalised when a listener is saved, so that they compare against the phone-number identities the bridge stores. A bare phone number (digits, optionally with a leading `+`, spaces or dashes) SHALL become `<digits>@s.whatsapp.net` for `chat_jids` and `<digits>` for `senders`; a JID with a device part SHALL lose it; a LID JID (`@lid`) SHALL be replaced by its phone-number JID when the mapping is known, and kept as given otherwise. At match time the message's chat and sender SHALL be the normalised values the bridge stores for it (LIDs already resolved to phone numbers where known).

#### Scenario: Phone number written with a plus sign
- **WHEN** a listener is created with `senders: ["+52 1 55 1234 5678"]`
- **THEN** it is stored as `senders: ["5215512345678"]` and fires for a message whose stored sender is `5215512345678`

#### Scenario: Sender writes from a LID
- **WHEN** a listener has `senders: ["5215512345678"]` and a message arrives whose sender is a LID that the bridge maps to `5215512345678@s.whatsapp.net`
- **THEN** the sender criterion matches

### Requirement: Criterion semantics
Each set criterion SHALL be evaluated against the message as stored by the bridge:
- `chat_jids` matches when the message's chat JID equals any entry.
- `senders` matches when the message's sender phone number (or JID) equals any entry.
- `contains` matches when the message text contains any entry, compared case-insensitively. The message text SHALL be the text the bridge stores, including image, video and document captions, with LID mentions rewritten to phone numbers.
- `regex` matches when the pattern finds a match in that same text, using RE2 syntax and case-sensitive unless the pattern says otherwise (for example `(?i)`).
- `mentions_me` matches when the message mentions the account itself, as defined in the "Mentions of the account" requirement.

A message with no text (for example a voice note) SHALL NOT match `contains` or `regex`.

#### Scenario: Contains is case-insensitive and reads captions
- **WHEN** a listener has `contains: ["guardia"]` and an image arrives with the caption "Mañana me toca GUARDIA"
- **THEN** the listener fires

#### Scenario: Regex respects its own flags
- **WHEN** a listener has `regex: "(?i)\\bincidente\\s+P[12]\\b"` and the text "Incidente p1 en producción" arrives
- **THEN** the listener fires

#### Scenario: Voice note does not match text criteria
- **WHEN** a listener has only `contains: ["hola"]` and a voice note with no text arrives
- **THEN** the listener does not fire

### Requirement: Match mode
A listener in `"or"` mode SHALL fire when at least one of its set criteria matches. A listener in `"and"` mode SHALL fire only when every set criterion matches. Criteria that are not set SHALL be ignored in both modes. Several values inside one list criterion SHALL always be alternatives (any of them), whatever the mode, so `chat_jids: [A, B]` in `"and"` mode means "in A or B, and every other set criterion".

#### Scenario: OR fires on either criterion
- **WHEN** a listener in `"or"` mode has `chat_jids: [Ops group]` and `contains: ["guardia"]`, and a message saying "guardia" arrives in another chat
- **THEN** the listener fires

#### Scenario: AND requires every criterion
- **WHEN** a listener in `"and"` mode has `chat_jids: [Ops group]` and `contains: ["guardia"]`, and a message saying "guardia" arrives in another chat
- **THEN** the listener does not fire

#### Scenario: AND fires when every criterion matches
- **WHEN** the same `"and"` listener sees "¿Quién está de guardia?" in the Ops group
- **THEN** the listener fires

#### Scenario: List values stay alternatives under AND
- **WHEN** a listener in `"and"` mode has `chat_jids: [A, B]` and `senders: [Ana]`, and Ana writes in chat B
- **THEN** the listener fires

### Requirement: Mentions of the account
A message SHALL count as mentioning the account when any JID in the message's mention list (`ContextInfo.MentionedJID` of the text, image, video, document or other content that carries it) refers to the account itself, either by its phone-number JID or by its LID, ignoring device parts. A LID mention SHALL be recognised whether or not it can be mapped to a phone number. Messages that only mention other people, or group-wide mentions that do not list the account, SHALL NOT count.

#### Scenario: Mention by LID
- **WHEN** a listener has `mentions_me: true` and a group message arrives whose `MentionedJID` holds the account's LID
- **THEN** the listener fires

#### Scenario: Mention by phone number
- **WHEN** a listener has `mentions_me: true` and a message's `MentionedJID` holds the account's phone-number JID with a device suffix
- **THEN** the listener fires

#### Scenario: Someone else is mentioned
- **WHEN** a listener has `mentions_me: true` and a message mentions only another participant
- **THEN** the listener does not fire

#### Scenario: Mention in a specific group
- **WHEN** a listener in `"and"` mode has `chat_jids: [Ops group]` and `mentions_me: true`, and someone mentions the account in that group
- **THEN** the listener fires, and a mention of the account in any other group does not fire it

### Requirement: Own messages are ignored by default
Messages sent by the account itself (from any linked device or the phone) SHALL NOT fire a listener whose `include_from_me` is `false`. When `include_from_me` is `true`, they SHALL be evaluated like any other message. This filter SHALL apply in both match modes, before the criteria are evaluated.

#### Scenario: Own message ignored
- **WHEN** the user writes "guardia" from their phone and a listener has `contains: ["guardia"]` with `include_from_me: false`
- **THEN** the listener does not fire

#### Scenario: Own message included on request
- **WHEN** the same message is seen by a listener with `include_from_me: true`
- **THEN** the listener fires and the delivery reports `is_from_me: true`

### Requirement: Only new, live messages fire listeners
Listeners SHALL be evaluated only for new messages received live by the bridge, after the bridge has stored them. The following SHALL NOT fire any listener: messages imported by history sync (initial or on-demand); edits of earlier messages; messages without text or media that the bridge does not store (reactions, receipts, protocol messages); status broadcasts (`status@broadcast`); and messages whose timestamp is older than the configured maximum age (`WEBHOOK_MAX_AGE`, default 15 minutes; `0` disables the check), which covers a backlog delivered after a long disconnection. Disabled listeners SHALL NOT fire.

#### Scenario: History sync does not trigger
- **WHEN** a history sync imports an old message from Ana and a listener has `senders: [Ana]`
- **THEN** the message is stored but no delivery is made

#### Scenario: Stale backlog does not trigger
- **WHEN** the bridge reconnects after two hours offline and receives a 90-minute-old message that matches a listener, with the default maximum age
- **THEN** no delivery is made

#### Scenario: Edit does not trigger again
- **WHEN** Ana edits a message that already fired a listener
- **THEN** no further delivery is made for the edit

#### Scenario: Disabled listener stays silent
- **WHEN** a matching message arrives for a listener with `enabled: false`
- **THEN** no delivery is made

### Requirement: At most one notification per listener and message
A listener SHALL fire at most once for a given message (chat JID and message ID) during the life of the bridge process, even when WhatsApp delivers the same message more than once. Several listeners matching the same message SHALL each fire once.

#### Scenario: Redelivered message
- **WHEN** WhatsApp delivers the same message ID twice within a minute and it matches a listener
- **THEN** exactly one delivery is queued for that listener

#### Scenario: Two listeners match
- **WHEN** a message matches two enabled listeners
- **THEN** one delivery is queued for each listener

### Requirement: Listener management through the REST API
The bridge SHALL expose these endpoints on its existing REST server, all answering JSON with a `success` flag:
- `POST /api/listeners` creates a listener and answers `201` with it.
- `GET /api/listeners` lists every listener, each with a summary of its last delivery (status and time) when there is one.
- `GET /api/listeners/{id}` returns one listener.
- `PATCH /api/listeners/{id}` updates the given fields, re-validates the result and answers `200`.
- `DELETE /api/listeners/{id}` deletes the listener and its delivery log.
- `POST /api/listeners/validate` validates a listener without storing it.
- `POST /api/listeners/{id}/test` sends a test delivery (see `webhook-delivery`).
- `GET /api/listeners/{id}/deliveries` lists the listener's most recent delivery records (default 20, at most 100).

An unknown `id` SHALL answer `404`; a wrong method SHALL answer `405`; a malformed body SHALL answer `400`. Every successful create, update or delete SHALL take effect for the next incoming message without a restart.

#### Scenario: Create, list and delete
- **WHEN** a client creates a listener, lists listeners, then deletes it
- **THEN** the create answers `201` with an `id`, the list includes it, the delete answers `200`, and a later matching message produces no delivery

#### Scenario: Disable through PATCH
- **WHEN** a client sends `PATCH /api/listeners/7` with `{"enabled": false}`
- **THEN** the bridge answers `200` with the updated listener and listener 7 stops firing immediately

#### Scenario: Unknown listener
- **WHEN** a client requests `GET /api/listeners/999` and no such listener exists
- **THEN** the bridge answers `404`

### Requirement: Protection of the management endpoints
Because the REST API has no general authentication, the listener endpoints SHALL refuse requests that a web browser could forge or that come from outside the machine without credentials:
- Requests carrying an `Origin` header SHALL be refused with `403`.
- Requests with a body SHALL be refused with `415` unless `Content-Type` is `application/json`.
- Requests whose `Host` header is not a loopback name or address, or the configured bind address, SHALL be refused with `403`.
- When `WEBHOOK_ADMIN_TOKEN` is set, every listener request SHALL present `Authorization: Bearer <token>`, compared in constant time, or be refused with `401`.
- When the API is bound to a non-loopback address (`BIND_ADDR`) and `WEBHOOK_ADMIN_TOKEN` is not set, the listener endpoints SHALL be refused with `403` and the bridge SHALL log a warning at start-up explaining why.

The existing `/api/send` and `/api/download` endpoints are outside this requirement.

#### Scenario: Cross-site request refused
- **WHEN** a web page in the user's browser posts a listener to `http://127.0.0.1:8080/api/listeners` with `Content-Type: text/plain`
- **THEN** the bridge refuses it and stores nothing

#### Scenario: Token required on a LAN binding
- **WHEN** the bridge runs with `BIND_ADDR=0.0.0.0` and no `WEBHOOK_ADMIN_TOKEN`
- **THEN** every listener endpoint answers `403` and no listener can be created

#### Scenario: Valid token accepted
- **WHEN** `WEBHOOK_ADMIN_TOKEN` is set and a local client presents it as a bearer token
- **THEN** the request is processed normally

### Requirement: Secrets are write-only
A listener's `secret` SHALL never be returned by any endpoint or MCP tool, nor written to logs. Responses SHALL instead carry `has_secret: true|false`. Sending `"secret": ""` in an update SHALL remove it; omitting the field SHALL keep it. `webhook_url` SHALL be returned with its query-string values masked, and logs SHALL show only its scheme and host.

#### Scenario: Secret hidden from listings
- **WHEN** a listener is created with a secret and then listed
- **THEN** the listing shows `has_secret: true` and no secret value

#### Scenario: Token in the URL masked
- **WHEN** a listener posts to `https://ntfy.example.org/alerts?auth=abc123` and is listed
- **THEN** the URL is shown as `https://ntfy.example.org/alerts?auth=***`

### Requirement: Listener management through MCP tools
The MCP server SHALL offer the tools `create_listener`, `list_listeners`, `delete_listener`, `set_listener_enabled` and `test_listener`, which call the bridge's listener endpoints and return their result, including validation errors, as structured data. When `WEBHOOK_ADMIN_TOKEN` is set in the MCP server's environment, the tools SHALL send it as a bearer token. The tools SHALL accept phone numbers and JIDs for chats and senders exactly as the REST API does.

#### Scenario: Create a listener from an MCP client
- **WHEN** an MCP client calls `create_listener` with `name: "Ana"`, `senders: ["5215512345678"]` and `webhook_url: "http://127.0.0.1:5678/webhook/wa"`
- **THEN** the tool returns `success: true` with the new listener's `id`, and the listener appears in `list_listeners`

#### Scenario: Validation errors reach the MCP client
- **WHEN** an MCP client calls `create_listener` with an invalid regex
- **THEN** the tool returns `success: false` with the bridge's `errors` list, and nothing is stored

#### Scenario: Disable from an MCP client
- **WHEN** an MCP client calls `set_listener_enabled` with `enabled: false`
- **THEN** the listener stops firing and `list_listeners` shows it disabled
