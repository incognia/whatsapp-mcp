## Purpose

Defines how a chat's last message and last activity time are determined, so the MCP tools always show a chat's real latest message, never list a chat twice, and keep its activity time consistent with what is stored.

## ADDED Requirements

### Requirement: The last message is the newest stored message
When the MCP tools report a chat's last message (`list_chats`, `get_chat` and `get_direct_chat_by_contact` with last messages included), they SHALL report the chat's stored message with the latest timestamp, regardless of the value stored as the chat's last activity time. When several stored messages of the chat share that latest timestamp, the tools SHALL pick one of them deterministically, so repeated calls on unchanged data report the same message.

#### Scenario: Chat time drifted after a discarded event
- **WHEN** a chat's newest stored message is "see you" at 10:00 and its last activity time was later set to 10:05 by an event that was not stored
- **THEN** `list_chats` reports "see you" as that chat's last message

#### Scenario: Two messages in the same second
- **WHEN** a chat's two newest stored messages share the same timestamp
- **THEN** `list_chats` reports exactly one of them as the last message, and the same one on every call

#### Scenario: Chat without stored messages
- **WHEN** a chat has no stored messages
- **THEN** the chat is still reported, with an empty last message, sender and from-me flag

### Requirement: Each chat is listed once
`list_chats` SHALL return each chat at most once per call, whether or not last messages are included and however many stored messages share the chat's latest timestamp.

#### Scenario: No duplicate rows
- **WHEN** `list_chats` is called with a limit larger than the number of chats
- **THEN** the number of returned chats equals the number of distinct chat JIDs returned

### Requirement: Only stored messages advance a chat's activity time
The bridge SHALL advance a chat's last activity time only when it stores an incoming message for that chat. An incoming event that is not stored, because it carries neither text nor media (for example a reaction or a protocol message), SHALL NOT change the chat's last activity time, but SHALL still create the chat, with its resolved name, if it does not exist yet.

#### Scenario: Reaction does not move the chat
- **WHEN** a chat's last activity time is 10:00 and a reaction to one of its messages arrives at 10:05
- **THEN** the chat's last activity time stays 10:00

#### Scenario: Text message moves the chat
- **WHEN** a text message is received and stored for a chat at 10:07
- **THEN** the chat's last activity time becomes 10:07, equal to that message's timestamp

#### Scenario: First event from a new chat is not storable
- **WHEN** the first event ever received from a chat is a reaction
- **THEN** the chat exists afterwards with its resolved name and no stored messages

### Requirement: Drifted activity times are repaired on startup
On startup, the bridge SHALL set the last activity time of every chat that has stored messages to the timestamp of its newest stored message when the two differ. Chats without stored messages SHALL keep their current last activity time. The repair SHALL NOT change chat names or messages.

#### Scenario: Chat ahead of its newest message
- **WHEN** the bridge starts and a chat's last activity time is 10:05 while its newest stored message is at 10:00
- **THEN** after startup the chat's last activity time is 10:00

#### Scenario: Chat without messages is left alone
- **WHEN** the bridge starts and a chat with no stored messages has a last activity time of 09:00
- **THEN** after startup its last activity time is still 09:00

### Requirement: Activity order follows the last message
Sorting chats by last activity SHALL use the chats' last activity time, and for every chat with stored messages that time SHALL equal the timestamp of the last message the tools report for it, once the startup repair has run.

#### Scenario: Most recent conversation first
- **WHEN** chat A's newest stored message is at 11:00 and chat B's at 10:00, and `list_chats` is called sorted by last activity
- **THEN** chat A is listed before chat B, and each shows its own newest message
