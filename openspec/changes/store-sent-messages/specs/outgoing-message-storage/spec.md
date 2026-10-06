## Purpose

Ensures that every message sent through the bridge's REST API is recorded in the local message store, so that the MCP tools that read the store (messages, chats, last interaction, media download) reflect the user's own sends immediately and consistently with received messages.

## ADDED Requirements

### Requirement: Successful sends are stored locally
When `/api/send` reports success, the bridge SHALL have written the sent message to the local `messages` table before returning the response, using the message ID and timestamp assigned by WhatsApp to that send. This SHALL apply to plain text sends, text sends with `mentions`, and media sends (image, audio, video and document) with or without a caption.

#### Scenario: Plain text send appears in the message list
- **WHEN** a client posts `{"recipient": "5215512345678", "message": "hello"}` to `/api/send` and the send succeeds
- **THEN** the `messages` table contains a row whose `id` is the WhatsApp message ID of that send, whose `chat_jid` is `5215512345678@s.whatsapp.net` and whose `content` is `hello`
- **AND** `list_messages` for that chat returns the message without waiting for any further event from WhatsApp

#### Scenario: Send with mentions is stored with its text
- **WHEN** a client posts a message containing `@5215512345678` with `mentions: ["5215512345678"]` to a group JID and the send succeeds
- **THEN** the stored row for that group has `content` equal to the sent text, including the `@5215512345678` mention

#### Scenario: Media send with a caption is stored with media metadata
- **WHEN** a client posts a `media_path` pointing to a JPEG file and a `message` used as caption, and the send succeeds
- **THEN** the stored row has `media_type` `image`, the caption as `content`, a generated `filename` of the form `image_<message time>_<last 8 characters of the ID>.jpg`, and the URL, media key, file hashes and file length of the uploaded media
- **AND** `download_media` with that message ID and chat JID can retrieve the file

#### Scenario: Media send without a caption is still stored
- **WHEN** a client posts only a `media_path` for a document and the send succeeds
- **THEN** a row is stored with `media_type` `document` and empty `content`

### Requirement: Stored own messages identify the user as sender
A stored sent message SHALL have `is_from_me` set to true and `sender` set to the phone-number user part of the linked account, the same value used for the user's own messages received through history sync, so the MCP server presents it as sent by "Me".

#### Scenario: Sender is the linked account
- **WHEN** a message is sent successfully from an account linked as `5215500000000`
- **THEN** the stored row has `is_from_me = 1` and `sender = 5215500000000`
- **AND** the MCP server formats it as `From: Me`

### Requirement: Chat bookkeeping follows sent messages
After a successful send, the bridge SHALL ensure a row exists in `chats` for the recipient chat and SHALL set its `last_message_time` to exactly the timestamp stored on the sent message. An existing non-empty chat name SHALL be preserved; for a new chat a name SHALL be resolved in the same way as for received messages.

#### Scenario: Existing chat moves to the top of the chat list
- **WHEN** a message is sent successfully to a chat that already exists with name `Alice` and an older `last_message_time`
- **THEN** the chat keeps the name `Alice`
- **AND** its `last_message_time` equals the sent message's timestamp, so `list_chats` sorted by last activity lists it first and shows the sent message as its last message

#### Scenario: First message to a new recipient
- **WHEN** a message is sent successfully to a phone number with no existing chat row
- **THEN** a chat row is created for that recipient before the message is stored, so the message is not rejected by the chat foreign key

### Requirement: Sent messages use the same chat addressing as received messages
The bridge SHALL store a sent message under the phone-number chat JID whenever the recipient is given as a LID JID with a known phone-number mapping, and SHALL strip any device part from the recipient JID, so that sent and received messages of one conversation share a single chat.

#### Scenario: Recipient given as a LID
- **WHEN** a client sends to `123456789012345@lid` and the bridge knows that LID maps to `5215512345678@s.whatsapp.net`
- **THEN** the sent message is stored with `chat_jid` `5215512345678@s.whatsapp.net`

#### Scenario: LID without a known mapping
- **WHEN** a client sends to a LID JID with no known phone-number mapping
- **THEN** the sent message is stored under the LID JID unchanged

### Requirement: Storage is idempotent with echoed copies
If the same sent message later arrives again as an event (for example, echoed by another linked device or replayed in a history sync), the bridge SHALL NOT create a duplicate row: the message SHALL remain a single row identified by its message ID and chat JID.

#### Scenario: Echo of a sent message
- **WHEN** a message is sent and stored, and an event carrying the same message ID for the same chat is then received
- **THEN** the `messages` table contains exactly one row for that message ID and chat
- **AND** that row still has `is_from_me = 1`

### Requirement: Failed sends are not stored
The bridge SHALL NOT store any message or update any chat when the send does not succeed, including when the recipient cannot be parsed, the media path is refused or unreadable, the media upload fails, or WhatsApp rejects the message.

#### Scenario: WhatsApp rejects the send
- **WHEN** `/api/send` returns `success: false` because sending to WhatsApp failed
- **THEN** no new row is written to `messages` and no `chats.last_message_time` is changed

#### Scenario: Refused media path
- **WHEN** `/api/send` refuses a `media_path` containing `..`
- **THEN** nothing is written to the message store

### Requirement: Storage failures do not report a delivered message as failed
If writing a successfully sent message to the local store fails, the bridge SHALL log a warning and SHALL still report the send as successful, because the message has already been delivered and reporting failure would invite a duplicate send.

#### Scenario: Database write fails after delivery
- **WHEN** WhatsApp accepts the message but the local database write returns an error
- **THEN** `/api/send` responds with `success: true`
- **AND** the bridge logs a warning describing the storage failure

### Requirement: The send API contract is unchanged
Storing sent messages SHALL NOT change the `/api/send` request fields, the response fields, or the HTTP status codes returned for successful and failed sends.

#### Scenario: Existing client keeps working
- **WHEN** an existing client posts the same request body it used before this change
- **THEN** it receives a response with the same fields and status code as before
