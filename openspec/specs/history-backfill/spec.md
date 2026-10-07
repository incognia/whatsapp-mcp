# history-backfill Specification

## Purpose

Lets the user fetch older messages for one specific WhatsApp chat on demand, through the bridge REST API and an MCP tool, so missing or older history can be recovered without unlinking and re-pairing the device.

## Requirements

### Requirement: Backfill request endpoint

The bridge SHALL expose `POST /api/history/backfill` on its REST API, accepting a JSON body with a required `chat_jid` (string) and an optional `count` (integer). The endpoint SHALL be served on the same bind address as the other bridge endpoints (loopback by default) and SHALL accept only the `POST` method for creating a request.

#### Scenario: Request for a chat with stored messages

- **WHEN** a client sends `POST /api/history/backfill` with `{"chat_jid": "5215512345678@s.whatsapp.net", "count": 50}` while the bridge is connected and the chat has stored messages
- **THEN** the bridge sends one on-demand history request for that chat to the user's primary phone
- **AND** responds with HTTP `202 Accepted` and a JSON body containing `success: true`, `status: "pending"`, the stored `chat_jid`, the JID form used for the request (`request_jid`), the requested `count`, and the anchor's `oldest_known_id` and `oldest_known_timestamp`

#### Scenario: Wrong method

- **WHEN** a client sends `PUT` or `DELETE` to `/api/history/backfill`
- **THEN** the bridge responds with HTTP `405 Method Not Allowed` and sends nothing to WhatsApp

#### Scenario: Malformed body

- **WHEN** a client sends a body that is not valid JSON or omits `chat_jid`
- **THEN** the bridge responds with HTTP `400 Bad Request` and a JSON error message, and sends nothing to WhatsApp

### Requirement: Anchor taken from the oldest stored message

The bridge SHALL request history older than the oldest message stored in `messages.db` for the requested chat. The anchor SHALL be the stored message with the earliest timestamp (ties broken deterministically by message ID) and a non-empty ID, and the request SHALL carry that message's ID, its timestamp in seconds and its `is_from_me` flag.

#### Scenario: Anchor selection

- **WHEN** a chat has stored messages with timestamps 2026-01-10, 2026-03-02 and 2026-05-20
- **THEN** the backfill request is anchored on the 2026-01-10 message's ID, timestamp and `is_from_me` value

#### Scenario: Repeated requests walk further back

- **WHEN** a backfill for a chat has completed and stored messages older than the previous anchor, and the client requests another backfill for the same chat
- **THEN** the new request is anchored on the new oldest stored message, so each request asks for an earlier window

### Requirement: Chats without stored messages

The bridge SHALL NOT fabricate an anchor. When the requested chat has no stored message with a non-empty ID, the bridge SHALL refuse the request.

#### Scenario: Chat with no messages

- **WHEN** a client requests a backfill for a chat JID that has no stored messages (whether or not it exists in the `chats` table)
- **THEN** the bridge responds with HTTP `404 Not Found` and a JSON body with `success: false` and an error code `no_anchor` explaining that at least one message in the chat is needed as a starting point
- **AND** sends nothing to WhatsApp

### Requirement: Count validation

The `count` field SHALL default to 50 when omitted. The bridge SHALL accept values from 1 to 200 inclusive and SHALL reject any other value without contacting WhatsApp.

#### Scenario: Default count

- **WHEN** a client omits `count`
- **THEN** the request sent to the phone asks for 50 messages and the response reports `count: 50`

#### Scenario: Count out of range

- **WHEN** a client sends `count` of 0, a negative number or a number greater than 200
- **THEN** the bridge responds with HTTP `400 Bad Request` stating the allowed range, and sends nothing to WhatsApp

### Requirement: Chat JID forms

The bridge SHALL accept `chat_jid` as a phone-number user JID (`@s.whatsapp.net`), a LID user JID (`@lid`) or a group JID (`@g.us`). Stored data SHALL stay keyed by the phone-number JID: a LID input SHALL be translated to its phone-number JID for the anchor lookup and for status reporting when the mapping is known. For a one-to-one chat whose LID is known, the request sent to WhatsApp SHALL address the chat by its LID; otherwise it SHALL use the phone-number JID. Group JIDs SHALL be used unchanged. Broadcast, status, newsletter and other JID types SHALL be rejected.

#### Scenario: Phone-number chat with a known LID

- **WHEN** a client requests a backfill for `5215512345678@s.whatsapp.net` and the bridge knows that number's LID
- **THEN** the anchor is read from the messages stored under `5215512345678@s.whatsapp.net`
- **AND** the request to WhatsApp addresses the chat by its LID, and the response reports that LID as `request_jid`

#### Scenario: Phone-number chat without a known LID

- **WHEN** a client requests a backfill for a phone-number chat whose LID is unknown
- **THEN** the request to WhatsApp addresses the chat by its phone-number JID

#### Scenario: LID given as input

- **WHEN** a client sends `chat_jid` as a LID whose phone number is known
- **THEN** the bridge reads the anchor from, and reports status under, the phone-number JID

#### Scenario: Group chat

- **WHEN** a client requests a backfill for a `@g.us` JID with stored messages
- **THEN** the request to WhatsApp uses the group JID unchanged

#### Scenario: Unsupported JID

- **WHEN** a client sends `status@broadcast`, a newsletter JID, or a string that is not a JID
- **THEN** the bridge responds with HTTP `400 Bad Request` and sends nothing to WhatsApp

### Requirement: Connection and login checks

The bridge SHALL refuse backfill requests when it is not connected to WhatsApp or the device is not logged in, and SHALL report a failure to send the request.

#### Scenario: Bridge disconnected

- **WHEN** a client requests a backfill while the WhatsApp connection is down or the device is logged out
- **THEN** the bridge responds with HTTP `503 Service Unavailable` and a JSON error stating that the bridge is not connected, and sends nothing

#### Scenario: Sending the request fails

- **WHEN** the bridge is connected but WhatsApp rejects or times out the peer request
- **THEN** the bridge responds with HTTP `502 Bad Gateway` and the error message, and does not record the request as pending

### Requirement: Asynchronous delivery

A backfill SHALL be asynchronous. The request endpoint SHALL return as soon as the request has been handed to WhatsApp, without waiting for the messages. The messages SHALL be delivered later by the phone as an on-demand history sync and SHALL be stored through the same processing as other history syncs, including LID-to-phone-number translation of chats, senders and mentions, caption extraction and media metadata.

#### Scenario: Messages arrive later

- **WHEN** a backfill request has been accepted and the phone answers a few seconds later with older messages for the chat
- **THEN** those messages are stored in `messages.db` under the chat's phone-number JID, with senders and mentions translated from LIDs where the mapping is known
- **AND** they become visible to the MCP server's existing read tools such as `list_messages`

#### Scenario: Phone offline

- **WHEN** a backfill request has been accepted but the primary phone does not answer
- **THEN** no messages are stored and the request's status eventually becomes `timed_out`

### Requirement: Backfill status reporting

The bridge SHALL expose `GET /api/history/backfill?chat_jid=<jid>` returning the status of the most recent backfill request for that chat since the bridge started. The status SHALL be one of `pending`, `completed` or `timed_out`, and SHALL include the request time, the requested count and anchor, and, once completed, the number of messages stored, the oldest stored timestamp after the backfill, and whether the phone reports that more history remains (`more_available`: `true`, `false` or `null` when unknown). A request SHALL become `timed_out` when no on-demand history for that chat arrives within 120 seconds. Status SHALL be kept in memory only.

#### Scenario: Completed backfill

- **WHEN** an on-demand history sync for a chat with a pending request is processed
- **THEN** `GET /api/history/backfill?chat_jid=<jid>` returns `status: "completed"` with `messages_stored`, `oldest_timestamp` and `more_available`

#### Scenario: No request yet

- **WHEN** a client queries the status of a chat for which no backfill has been requested since the bridge started
- **THEN** the bridge responds with HTTP `404 Not Found` and `status: "none"`

#### Scenario: Timeout

- **WHEN** 120 seconds pass after an accepted request without an on-demand history sync for that chat
- **THEN** the status becomes `timed_out`

### Requirement: Idempotent storage of backfilled messages

Storing backfilled messages SHALL be idempotent: a message SHALL be identified by its message ID and chat JID, and storing a message that already exists SHALL replace it rather than create a duplicate. Storing an older batch SHALL NOT move the chat's last message time backwards and SHALL NOT clear an existing chat name.

#### Scenario: Overlapping batches

- **WHEN** two backfill responses for the same chat contain some of the same messages
- **THEN** each message appears exactly once in `messages.db`

#### Scenario: Older batch does not reorder chats

- **WHEN** a backfill stores messages from 2025 for a chat whose last message time is 2026-10-05
- **THEN** the chat's last message time remains 2026-10-05 and its position in `list_chats` does not change

### Requirement: Rate limiting

The bridge SHALL limit backfill traffic to reduce the risk of the account being flagged for automated behaviour. It SHALL refuse a new request for a chat while that chat's previous request is still `pending`, or within 30 seconds of that chat's previous request, and SHALL refuse any request made within 5 seconds of the previous accepted request for any chat. Refusals SHALL use HTTP `429 Too Many Requests` with a `Retry-After` header in seconds and SHALL send nothing to WhatsApp.

#### Scenario: Repeated request for the same chat

- **WHEN** a client requests a backfill for a chat 10 seconds after an accepted request for the same chat
- **THEN** the bridge responds with HTTP `429` and a `Retry-After` value of at least 20 seconds

#### Scenario: Requests for different chats in quick succession

- **WHEN** a client requests backfills for two different chats 2 seconds apart
- **THEN** the second request is refused with HTTP `429` and a `Retry-After` value of at least 3 seconds

### Requirement: MCP tool for requesting history

The MCP server SHALL provide a tool `request_chat_history` with parameters `chat_jid` (required), `count` (default 50) and `wait_seconds` (default 20, from 0 to 60). The tool SHALL call the bridge's backfill endpoint and, when `wait_seconds` is greater than 0, SHALL poll the status endpoint until the request is no longer pending or the wait expires. Its result SHALL state whether the request was accepted, the final or current status, the number of messages stored when known, whether more history may be available, and that messages arriving later can be read with `list_messages`. Bridge errors (not connected, no anchor, rate limited, invalid input, bridge unreachable) SHALL be returned as a result with `success: false` and the bridge's message, not raised as an exception.

#### Scenario: Messages arrive within the wait

- **WHEN** the tool is called with `wait_seconds` 20 and the phone answers within 10 seconds
- **THEN** the tool returns `success: true`, `status: "completed"` and the number of messages stored

#### Scenario: Still pending after the wait

- **WHEN** the tool is called and the phone has not answered by the end of `wait_seconds`
- **THEN** the tool returns `success: true`, `status: "pending"` and a note that messages may still arrive and can be checked later with `list_messages`

#### Scenario: Bridge reports an error

- **WHEN** the bridge answers `429`, `404`, `400` or `503`, or cannot be reached
- **THEN** the tool returns `success: false` with a human-readable message, including the retry delay for `429`
