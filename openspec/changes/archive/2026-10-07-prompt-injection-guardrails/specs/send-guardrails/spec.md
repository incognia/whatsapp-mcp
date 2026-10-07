## Purpose

Limits, enforced by the bridge where a prompt-injected model cannot argue with them, on what can be sent out of the user's account and on standing channels such as webhooks.

## ADDED Requirements

### Requirement: Read-only mode
When the bridge starts with `WHATSAPP_READ_ONLY` set to `true` or `1`, it SHALL refuse `/api/send` and the listener create, update and test-delivery endpoints with `403` and a message naming the setting, without contacting WhatsApp or changing any listener. Reading, media download, history backfill, listing and deleting listeners SHALL keep working, and existing enabled listeners SHALL keep firing. The bridge SHALL state at start-up whether read-only mode is on.

#### Scenario: Send refused
- **WHEN** the bridge runs with `WHATSAPP_READ_ONLY=true` and a client calls `/api/send`
- **THEN** the bridge answers `403`, sends nothing and stores nothing

#### Scenario: Listener creation refused
- **WHEN** the bridge runs with `WHATSAPP_READ_ONLY=true` and a client creates a listener
- **THEN** the bridge answers `403` and stores nothing

#### Scenario: Deleting still allowed
- **WHEN** the bridge runs with `WHATSAPP_READ_ONLY=true` and a client deletes a listener
- **THEN** the listener is deleted

### Requirement: Protected files are never sent
The bridge SHALL refuse a `media_path` that, after resolving symlinks, is inside its own `store/` folder, whatever `WHATSAPP_MEDIA_ROOTS` says. It SHALL also refuse a `media_path` with any path component starting with `.` (such as `~/.ssh/id_ed25519`, `~/.aws/credentials`, `project/.env`), unless the resolved path is inside a `WHATSAPP_MEDIA_ROOTS` entry that itself contains that component. Refusals SHALL answer `400` with a reason and SHALL NOT read the file.

#### Scenario: Session database refused
- **WHEN** a client sends with `media_path` set to the bridge's `store/whatsapp.db`
- **THEN** the bridge answers `400` and sends nothing

#### Scenario: SSH key refused
- **WHEN** a client sends with `media_path: "/Users/ana/.ssh/id_ed25519"` and `WHATSAPP_MEDIA_ROOTS` is not set
- **THEN** the bridge answers `400` and sends nothing

#### Scenario: Ordinary document allowed
- **WHEN** a client sends with `media_path: "/Users/ana/Documents/agenda.pdf"` and `WHATSAPP_MEDIA_ROOTS` is not set
- **THEN** the file is sent as today

### Requirement: Send rate limit
When `WHATSAPP_SEND_RATE` is set as `<per minute>/<per hour>` (for example `10/60`), the bridge SHALL accept at most that many sends per minute and per hour across all recipients; a value of `0` for either window SHALL leave that window unlimited. When it is not set, sends SHALL NOT be rate-limited. A send over the limit SHALL be refused with `429` and a `Retry-After` header, without contacting WhatsApp. Only sends that pass every other check SHALL count.

#### Scenario: No limit by default
- **WHEN** `WHATSAPP_SEND_RATE` is not set and a client makes 30 valid sends within one minute
- **THEN** all 30 are sent

#### Scenario: Burst stopped
- **WHEN** the bridge starts with `WHATSAPP_SEND_RATE=10/60` and a client makes 11 valid sends within one minute
- **THEN** the first 10 are sent and the 11th is answered `429` with `Retry-After`

#### Scenario: Custom limit
- **WHEN** the bridge starts with `WHATSAPP_SEND_RATE=2/5` and a client makes 3 sends within one minute
- **THEN** the third is answered `429`

### Requirement: Optional recipient allowlist
When `WHATSAPP_SEND_ALLOWED` is set (comma-separated phone numbers or JIDs), the bridge SHALL refuse with `403` any send whose recipient, after the same normalisation and LID resolution used for storing messages, is not on the list. When it is not set, any recipient SHALL be allowed.

#### Scenario: Recipient not on the list
- **WHEN** `WHATSAPP_SEND_ALLOWED=5215500000001,120363000000000000@g.us` and a client sends to `5215500000009`
- **THEN** the bridge answers `403` and sends nothing

#### Scenario: Group on the list
- **WHEN** the same list is set and a client sends to `120363000000000000@g.us`
- **THEN** the message is sent
