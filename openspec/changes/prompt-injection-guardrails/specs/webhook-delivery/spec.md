## MODIFIED Requirements

### Requirement: Webhook URL safety policy
A `webhook_url` SHALL be accepted only when: its scheme is `http` or `https`; it has a host and no user name or password; it is at most 2,048 characters; it uses `https` unless its host is a loopback or private-network (RFC 1918, RFC 4193) address or the name `localhost`; it does not point at the bridge's own REST API address and port; and its host is allowed: when `WEBHOOK_ALLOWED_HOSTS` (a comma-separated list of host names, `*.domain` suffixes or IP addresses) is not set, only loopback and private-network addresses and the name `localhost` are allowed; when it is set, only the hosts on that list are allowed. A public host SHALL therefore be refused unless it is listed, so that a prompt-injected listener cannot post messages to the internet. Every connection SHALL also check the resolved IP address and refuse link-local addresses (including `169.254.169.254`), unspecified, multicast and broadcast addresses, the bridge's own API address, and public addresses for a host that is not on `WEBHOOK_ALLOWED_HOSTS`, so that a host name that later resolves somewhere else cannot bypass the policy. Existing listeners that no longer satisfy the policy SHALL keep their configuration, and their deliveries SHALL fail with an error naming `WEBHOOK_ALLOWED_HOSTS`.

#### Scenario: Non-HTTP scheme rejected
- **WHEN** a listener is created with `webhook_url: "file:///etc/passwd"` or `"gopher://example.com"`
- **THEN** the bridge answers `400` and stores nothing

#### Scenario: Plain HTTP to a public host rejected
- **WHEN** a listener is created with `webhook_url: "http://hooks.example.com/wa"`
- **THEN** the bridge answers `400` explaining that public hosts require `https`

#### Scenario: Public host refused by default
- **WHEN** `WEBHOOK_ALLOWED_HOSTS` is not set and a listener is created with `webhook_url: "https://hooks.example.com/wa"`
- **THEN** the bridge answers `400` explaining that public hosts must be listed in `WEBHOOK_ALLOWED_HOSTS`, and stores nothing

#### Scenario: Public host allowed when listed
- **WHEN** `WEBHOOK_ALLOWED_HOSTS=hooks.example.com` and a listener is created with `webhook_url: "https://hooks.example.com/wa"`
- **THEN** the listener is accepted

#### Scenario: Local automation allowed
- **WHEN** `WEBHOOK_ALLOWED_HOSTS` is not set and a listener is created with `webhook_url: "http://127.0.0.1:5678/webhook/wa"`
- **THEN** the listener is accepted

#### Scenario: Bridge cannot call itself
- **WHEN** a listener is created with `webhook_url: "http://127.0.0.1:8080/api/send"` while the bridge listens on port 8080
- **THEN** the bridge answers `400` and stores nothing

#### Scenario: Cloud metadata address refused at connection time
- **WHEN** a listener's host name resolves to `169.254.169.254` at delivery time
- **THEN** the bridge does not connect and records the delivery as failed

#### Scenario: Host name refused without an allowlist
- **WHEN** `WEBHOOK_ALLOWED_HOSTS` is not set and a listener is created with `webhook_url: "https://n8n.home.example/hook"`
- **THEN** the bridge answers `400` naming `WEBHOOK_ALLOWED_HOSTS`, because a name cannot be known to be local before it is resolved

#### Scenario: Public address refused at connection time
- **WHEN** `WEBHOOK_ALLOWED_HOSTS` is not set and a delivery would connect to a public address
- **THEN** the bridge does not connect and records the delivery as failed

#### Scenario: Allowlist enforced
- **WHEN** `WEBHOOK_ALLOWED_HOSTS=n8n.example.org,127.0.0.1` and a listener targets `https://other.example.net/hook`
- **THEN** the bridge answers `400` naming the allowlist

#### Scenario: Existing public listener after upgrade
- **WHEN** a listener created before this change targets `https://hooks.example.com/wa` and `WEBHOOK_ALLOWED_HOSTS` is not set
- **THEN** the listener is kept, and each delivery is recorded as failed with an error naming `WEBHOOK_ALLOWED_HOSTS`
