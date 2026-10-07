## Purpose

Delivers each listener match to the listener's webhook as a signed JSON HTTP request, asynchronously, with bounded retries and without ever slowing WhatsApp message handling or opening the bridge to SSRF or silent data exfiltration.

## ADDED Requirements

### Requirement: One webhook request per match
For every message that fires a listener, the bridge SHALL send an HTTP `POST` to that listener's `webhook_url` with a JSON body and the headers `Content-Type: application/json`, `User-Agent: whatsapp-bridge-webhook/1`, `X-Webhook-Event`, `X-Webhook-Delivery` (a unique delivery ID that stays the same across retries) and `X-Webhook-Timestamp` (Unix seconds at the time of the attempt).

#### Scenario: Matching message delivered
- **WHEN** a message from Amelia fires the listener "Amelia" whose URL is `http://127.0.0.1:5678/webhook/wa`
- **THEN** the receiver gets one `POST` with `X-Webhook-Event: message` and a JSON body describing the message

### Requirement: Payload shape
The body of a message delivery SHALL be a JSON object with this shape, and fields SHALL NOT be removed or renamed without a new `version`:

```json
{
  "version": 1,
  "event": "message",
  "delivery_id": "<unique id>",
  "listener": {"id": 7, "name": "Amelia"},
  "match_mode": "or",
  "matched": ["senders"],
  "message": {
    "id": "<WhatsApp message id>",
    "chat_jid": "5215512345678@s.whatsapp.net",
    "chat_name": "Amelia",
    "is_group": false,
    "sender": "5215512345678",
    "sender_jid": "5215512345678@s.whatsapp.net",
    "sender_name": "Amelia",
    "timestamp": "2026-10-06T18:04:05Z",
    "content": "¿Quién está de guardia?",
    "media_type": "",
    "filename": "",
    "is_from_me": false,
    "mentions_me": false
  }
}
```

`matched` SHALL list the names of the set criteria that matched (`chat_jids`, `senders`, `contains`, `regex`, `mentions_me`), in that order. Chat and sender SHALL be the normalised phone-number forms the bridge stores; `chat_name` and `sender_name` SHALL be the best known names (contact name, then push name, then the phone number). `timestamp` SHALL be RFC 3339 in UTC. `content` SHALL be the stored text (captions included, LID mentions rewritten) and SHALL be at most 4,096 characters, truncated with `content_truncated: true` when longer. Media SHALL NOT be embedded; `media_type` and `filename` let the receiver fetch it through the existing download path.

#### Scenario: Group message payload
- **WHEN** Amelia writes in the DevSecOps group and a listener on that group fires
- **THEN** the payload has `is_group: true`, `chat_jid` ending in `@g.us`, `chat_name` set to the group name, and `sender`/`sender_name` identifying Amelia

#### Scenario: Image with caption
- **WHEN** an image with a caption fires a listener
- **THEN** the payload has `media_type: "image"`, the generated `filename`, and the caption as `content`

### Requirement: Request signature
When a listener has a secret, every request (including retries and test deliveries) SHALL carry `X-Webhook-Signature: sha256=<hex>`, where `<hex>` is the lower-case hexadecimal HMAC-SHA256, keyed with the secret, of the `X-Webhook-Timestamp` value, a full stop, and the exact request body bytes. When a listener has no secret, the header SHALL be omitted. The secret SHALL be at least 16 characters when present.

#### Scenario: Receiver verifies the signature
- **WHEN** a listener has the secret `s3cr3t-0123456789` and a delivery is made with timestamp `1791331445`
- **THEN** `X-Webhook-Signature` equals `sha256=` followed by HMAC-SHA256(`s3cr3t-0123456789`, `"1791331445." + body`) in hex

#### Scenario: Short secret rejected
- **WHEN** a listener is created with `secret: "abc"`
- **THEN** the bridge answers `400` and stores nothing

### Requirement: Timeouts, retries and success
Each attempt SHALL time out after `WEBHOOK_TIMEOUT` (default 10 seconds) including connection, TLS and response headers. A `2xx` response SHALL mark the delivery as delivered. A network error, a timeout, `408`, `429` or any `5xx` SHALL be retried, up to 4 attempts in total, waiting about 2, 10 and 30 seconds (with up to 20% jitter) before the second, third and fourth attempts; a `Retry-After` header SHALL be honoured when it asks for no more than 60 seconds. Any other `4xx` SHALL end the delivery as failed without retrying. Redirects SHALL NOT be followed and SHALL count as a failure without retry. At most 4 KiB of a response body SHALL be read. A retry SHALL re-sign the body with a fresh timestamp and keep the same delivery ID.

#### Scenario: Transient failure then success
- **WHEN** the receiver answers `503` to the first attempt and `200` to the second
- **THEN** the delivery is recorded as delivered after 2 attempts, both carrying the same `X-Webhook-Delivery`

#### Scenario: Permanent client error
- **WHEN** the receiver answers `404`
- **THEN** the delivery is recorded as failed after 1 attempt and is not retried

#### Scenario: Receiver never answers
- **WHEN** the receiver accepts the connection but never responds
- **THEN** each attempt is abandoned after the timeout and the delivery is recorded as failed after 4 attempts

#### Scenario: Redirect not followed
- **WHEN** the receiver answers `302` pointing at another host
- **THEN** the bridge does not contact that host and records the delivery as failed

### Requirement: Asynchronous, bounded delivery
Matching and queueing SHALL happen without network I/O in the path that handles WhatsApp events, and delivery SHALL be done by a fixed pool of background workers (`WEBHOOK_WORKERS`, default 2) fed by a bounded queue (`WEBHOOK_QUEUE_SIZE`, default 256). When the queue is full, the new delivery SHALL be dropped, recorded as `dropped`, and counted in a warning log that is emitted at most once per minute; the event handler MUST NOT wait for space. Waiting between retries MUST NOT occupy a worker. Delivery order across messages is not guaranteed. On shutdown the bridge SHALL stop accepting new deliveries, give in-flight attempts up to 5 seconds to finish, and drop the rest.

#### Scenario: Slow receiver does not delay message storage
- **WHEN** a listener's receiver takes 10 seconds to answer and 20 matching messages arrive in a burst
- **THEN** all 20 messages are stored as they arrive, without waiting for any delivery

#### Scenario: Queue overflow drops instead of blocking
- **WHEN** the queue is full and another matching message arrives
- **THEN** the message is stored, its delivery is recorded as `dropped`, and a warning is logged

### Requirement: Webhook URL safety policy
A `webhook_url` SHALL be accepted only when: its scheme is `http` or `https`; it has a host and no user name or password; it is at most 2,048 characters; it uses `https` unless its host is a loopback or private-network (RFC 1918, RFC 4193) address or the name `localhost`; it does not point at the bridge's own REST API address and port; and, when `WEBHOOK_ALLOWED_HOSTS` is set (a comma-separated list of host names, `*.domain` suffixes or IP addresses), its host is on that list. Every connection SHALL also check the resolved IP address and refuse link-local addresses (including `169.254.169.254`), unspecified, multicast and broadcast addresses, and the bridge's own API address, so that a host name that later resolves somewhere else cannot bypass the policy.

#### Scenario: Non-HTTP scheme rejected
- **WHEN** a listener is created with `webhook_url: "file:///etc/passwd"` or `"gopher://example.com"`
- **THEN** the bridge answers `400` and stores nothing

#### Scenario: Plain HTTP to a public host rejected
- **WHEN** a listener is created with `webhook_url: "http://hooks.example.com/wa"`
- **THEN** the bridge answers `400` explaining that public hosts require `https`

#### Scenario: Local automation allowed
- **WHEN** a listener is created with `webhook_url: "http://127.0.0.1:5678/webhook/wa"`
- **THEN** the listener is accepted

#### Scenario: Bridge cannot call itself
- **WHEN** a listener is created with `webhook_url: "http://127.0.0.1:8080/api/send"` while the bridge listens on port 8080
- **THEN** the bridge answers `400` and stores nothing

#### Scenario: Cloud metadata address refused at connection time
- **WHEN** a listener's host name resolves to `169.254.169.254` at delivery time
- **THEN** the bridge does not connect and records the delivery as failed

#### Scenario: Allowlist enforced
- **WHEN** `WEBHOOK_ALLOWED_HOSTS=n8n.example.org,127.0.0.1` and a listener targets `https://other.example.net/hook`
- **THEN** the bridge answers `400` naming the allowlist

### Requirement: Delivery log
The bridge SHALL record each delivery in `store/messages.db` with: listener ID, delivery ID, event (`message` or `test`), message ID and chat JID, status (`delivered`, `failed` or `dropped`), number of attempts, last HTTP status code, a short error description, and the creation and completion times. The record SHALL NOT contain the payload, the message content, the secret, the signature or the full URL. The log SHALL be pruned to the most recent 1,000 records, and deleting a listener SHALL delete its records.

#### Scenario: Failed delivery is visible
- **WHEN** a delivery ends as failed after 4 attempts with status `503`
- **THEN** `GET /api/listeners/{id}/deliveries` shows it with `status: "failed"`, `attempts: 4` and `status_code: 503`, and without the message content

### Requirement: Test delivery
`POST /api/listeners/{id}/test` SHALL send one `POST` with `X-Webhook-Event: test` and a payload of the normal shape whose `event` is `"test"` and whose `message` holds clearly fictitious values, signed like any delivery, synchronously and without retries, and SHALL answer with the outcome (`delivered` or `failed`, the status code and a short error). It SHALL follow the URL safety policy and be recorded in the delivery log. It SHALL work for disabled listeners.

#### Scenario: Test reaches the receiver
- **WHEN** a client tests a listener whose receiver answers `200`
- **THEN** the endpoint answers `success: true` with `status: "delivered"` and the receiver gets an `event: "test"` payload

#### Scenario: Test reports failure
- **WHEN** a client tests a listener whose receiver is down
- **THEN** the endpoint answers `success: false` with `status: "failed"` and a connection error description

### Requirement: Logging hygiene
Bridge logs about webhooks SHALL identify listeners by ID and name and targets by scheme and host only. They SHALL NOT include secrets, signatures, bearer tokens, URL paths or query strings, request bodies or response bodies.

#### Scenario: Failure log omits sensitive parts
- **WHEN** a delivery to `https://hooks.example.org/T000/B000/XXXX?token=abc` fails
- **THEN** the log line names `https://hooks.example.org` and the listener, and contains neither the path, the token, nor the message text
