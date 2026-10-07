## Purpose

Keeps the content of the user's conversations out of the bridge's console output by default, while still showing enough activity to operate and debug the bridge, with an explicit opt-in for content when debugging locally.

## ADDED Requirements

### Requirement: Message content is not logged by default
Unless content logging is enabled, the bridge SHALL NOT write the text of any message, any media caption, any media filename or any local file path to its console output. This SHALL apply to live incoming messages, live messages sent from the user's other devices, messages sent through the bridge's REST API, and messages imported by history sync (initial or on-demand).

#### Scenario: Live incoming message
- **WHEN** a message with the text "nos vemos a las 8" arrives and content logging is disabled
- **THEN** the console output contains no line with "nos vemos a las 8"

#### Scenario: Message sent through the API with a file
- **WHEN** a client calls `/api/send` with a text and a `media_path` of `/Users/me/Documents/contract.pdf` and content logging is disabled
- **THEN** the console output contains neither the text nor the path

#### Scenario: History sync
- **WHEN** a history sync stores 50 messages and content logging is disabled
- **THEN** none of their texts, captions or filenames appear in the console output

### Requirement: Activity remains visible as metadata
With content logging disabled, the bridge SHALL still log one line per stored live message and per message sent through the REST API, showing the time, the direction (incoming or outgoing), the chat, the sender, the media type when there is one, and the number of characters of the text. For history sync it SHALL log one summary line per conversation with the chat, the number of messages stored and the time range covered, instead of lines per message.

#### Scenario: Live message metadata line
- **WHEN** a 17-character text message arrives in a group and content logging is disabled
- **THEN** the console shows a line with the time, an incoming marker, the group JID, the sender and "17 chars", and nothing of the text

#### Scenario: History sync summary
- **WHEN** a history sync stores 19 messages for one chat
- **THEN** the console shows one line for that chat with 19 messages and the oldest and newest times, and no per-message lines

### Requirement: Content logging is an explicit opt-in
Setting the environment variable `WHATSAPP_LOG_CONTENT` to `true` (or `1`) SHALL make the per-message lines of the "Activity remains visible as metadata" requirement also include the text and, for media, the filename; history sync SHALL then also log one line per stored message with its text. Any other value, or no value, SHALL keep content out of the logs. The bridge SHALL state at start-up whether content logging is enabled. Enabling content logging SHALL NOT change the verbosity of any other log output.

#### Scenario: Opt-in for debugging
- **WHEN** the bridge starts with `WHATSAPP_LOG_CONTENT=true` and a message "hola" arrives
- **THEN** the start-up output says content logging is on, and the message's log line includes "hola"

#### Scenario: Unrecognised value
- **WHEN** the bridge starts with `WHATSAPP_LOG_CONTENT=yes-please`
- **THEN** content logging stays off and the start-up output says so

### Requirement: Storage and interfaces are unaffected
Changing what is logged SHALL NOT change what the bridge stores, what the REST API returns, or what the MCP tools return.

#### Scenario: Stored data unchanged
- **WHEN** a message arrives with content logging disabled
- **THEN** it is stored with its full text and is returned in full by `list_messages`
