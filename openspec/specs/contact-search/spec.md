# contact-search Specification

## Purpose

Lets the assistant find WhatsApp contacts and chats by name or phone number the way a person types them: without accents, in any case and in any word order, across the whole address book rather than only chats with message history.

## Requirements

### Requirement: Accent- and case-insensitive name matching
The `search_contacts` tool SHALL compare the query against contact names after removing diacritics (accents, tildes, umlauts and other combining marks) and folding case on both sides, so that a query written with or without accents, in any letter case, matches the same contacts.

#### Scenario: Query without accents finds an accented name
- **WHEN** the address book contains "Rubén García" and `search_contacts` is called with "ruben"
- **THEN** the results include "Rubén García"

#### Scenario: Accented query finds an unaccented name
- **WHEN** a chat is named "Jose Ramirez" and `search_contacts` is called with "José"
- **THEN** the results include "Jose Ramirez"

#### Scenario: Case is ignored
- **WHEN** the address book contains "Maayan Levy" and `search_contacts` is called with "MAAYAN"
- **THEN** the results include "Maayan Levy"

#### Scenario: Tilde on n is ignored
- **WHEN** the address book contains "Begoña Peña" and `search_contacts` is called with "begona pena"
- **THEN** the results include "Begoña Peña"

### Requirement: Multi-word queries match in any order
The `search_contacts` tool SHALL split the query on whitespace into words and SHALL return a contact only when every word occurs, as a substring after normalisation, in at least one of that contact's names or in its phone number; the order of the words in the query MUST NOT affect which contacts match. A word matched in one name field and another word matched in a different name field of the same contact SHALL together count as a match.

#### Scenario: Reversed word order
- **WHEN** the address book contains "Rubén García" and `search_contacts` is called with "garcia ruben"
- **THEN** the results include "Rubén García"

#### Scenario: Partial words
- **WHEN** the address book contains "Rubén García" and `search_contacts` is called with "rub garc"
- **THEN** the results include "Rubén García"

#### Scenario: Every word must match
- **WHEN** the address book contains "Rubén García" and "Rubén Torres" and `search_contacts` is called with "ruben torres"
- **THEN** the results include "Rubén Torres" and do not include "Rubén García"

#### Scenario: Words spread across name fields
- **WHEN** a contact has the saved name "Rubén" and the WhatsApp profile name "Rubén García" and `search_contacts` is called with "garcia ruben"
- **THEN** the results include that contact

### Requirement: Phone number matching
The `search_contacts` tool SHALL match contacts by phone number, ignoring a leading `+`, spaces, hyphens, dots and parentheses in the query when the query consists only of digits and those characters.

#### Scenario: Partial phone number
- **WHEN** a contact has the phone number 5215512345678 and `search_contacts` is called with "5512345"
- **THEN** the results include that contact

#### Scenario: Formatted phone number
- **WHEN** a contact has the phone number 5215512345678 and `search_contacts` is called with "+52 1 55 1234 5678"
- **THEN** the results include that contact

### Requirement: Address-book contacts are searchable
The `search_contacts` tool SHALL search both the individual chats stored by the bridge and every contact in WhatsApp's own contact store (saved full name, first name, WhatsApp profile name and business name), so that contacts without any message history are found.

#### Scenario: Contact without message history
- **WHEN** "Maayan Levy" exists in the contact store but has no chat in the message store, and `search_contacts` is called with "maayan"
- **THEN** the results include "Maayan Levy" with its phone number and phone-number JID

#### Scenario: Chat named only by its number
- **WHEN** a chat's stored name is its bare phone number and the contact store has the full name "Rubén García" for the same JID, and `search_contacts` is called with "ruben"
- **THEN** the results include that contact with the name "Rubén García"

#### Scenario: Contact store not available
- **WHEN** the contact store database file is missing, unreadable or locked, and `search_contacts` is called
- **THEN** the tool returns matches from the chats in the message store only, without raising an error

### Requirement: LID-keyed contacts resolve to phone numbers
The `search_contacts` tool SHALL report contacts that the contact store keys by a LID JID (`@lid`) under their phone-number JID (`@s.whatsapp.net`) when the LID-to-phone-number mapping is known, and SHALL NOT return LID JIDs or LID numbers as a contact's `jid` or `phone_number`. Contacts keyed only by a LID with no known phone-number mapping SHALL be omitted.

#### Scenario: LID contact with known mapping
- **WHEN** the contact store holds "Rubén García" under a LID JID that maps to phone number 5215512345678, and `search_contacts` is called with "ruben"
- **THEN** the result for that contact has `jid` "5215512345678@s.whatsapp.net" and `phone_number` "5215512345678"

#### Scenario: LID contact without mapping
- **WHEN** the contact store holds a contact only under a LID JID with no known phone-number mapping
- **THEN** that contact does not appear in `search_contacts` results

### Requirement: One result per contact
The `search_contacts` tool SHALL return at most one result per phone-number JID, merging the chat entry, the phone-number contact entry and any LID contact entry for the same person. The reported `name` SHALL be the first non-empty value among: the address-book full name, the chat name (unless it is just the bare phone number), the first name, the business name and the WhatsApp profile name; when none exists, the phone number SHALL be reported as the name.

#### Scenario: Same person in several sources
- **WHEN** "Rubén García" exists as a chat, as a phone-number contact and as a LID contact mapped to the same phone number, and `search_contacts` is called with "ruben"
- **THEN** exactly one result for that phone-number JID is returned

#### Scenario: Saved name preferred over profile name
- **WHEN** a contact has the saved full name "Rubén García" and the WhatsApp profile name "Rubo"
- **THEN** its result reports the name "Rubén García"

#### Scenario: Both names remain searchable
- **WHEN** a contact has the saved full name "Rubén García" and the WhatsApp profile name "Rubo", and `search_contacts` is called with "rubo"
- **THEN** the results include that contact, reported with the name "Rubén García"

### Requirement: Groups excluded from contact search
The `search_contacts` tool SHALL NOT return group chats (`@g.us`), broadcast lists or status JIDs.

#### Scenario: Group with a matching name
- **WHEN** a group named "Familia García" exists and `search_contacts` is called with "garcia"
- **THEN** the group is not among the results

### Requirement: Result shape, limit and ordering
The `search_contacts` tool SHALL keep its existing input (`query`) and result fields (`phone_number`, `name`, `jid`), SHALL return at most 50 results, and SHALL order results deterministically: contacts whose every query word matches the start of a word come before contacts matched only inside a word; within each group, contacts with a chat in the message store come before address-book-only contacts; ties are ordered by normalised name and then by JID. A query that is empty or only whitespace SHALL return an empty list.

#### Scenario: Result limit
- **WHEN** more than 50 contacts match the query
- **THEN** exactly 50 results are returned

#### Scenario: Word-start matches first
- **WHEN** "Ana López" and "Mariana Ruiz" both match the query "ana"
- **THEN** "Ana López" is listed before "Mariana Ruiz"

#### Scenario: Stable order
- **WHEN** `search_contacts` is called twice with the same query and unchanged data
- **THEN** both calls return the same results in the same order

#### Scenario: Blank query
- **WHEN** `search_contacts` is called with "   "
- **THEN** an empty list is returned

### Requirement: Read-only access to the contact store
The MCP server SHALL open WhatsApp's contact store database in read-only mode and MUST NOT create, modify, lock for writing or migrate it, so that the bridge, which owns that database, is never disturbed.

#### Scenario: Searching does not change the contact store
- **WHEN** `search_contacts` is called any number of times
- **THEN** the contact store database file and its tables are unchanged, and no file is created if the database does not exist

### Requirement: Chat name filter uses the same matching
The `query` filter of the `list_chats` tool SHALL match a chat when every query word, normalised as for `search_contacts`, occurs in the chat's name, in its JID or, for an individual chat, in its contact's names from the contact store; it SHALL keep the existing `limit`, `page`, `include_last_message` and `sort_by` behaviour, applying pagination after filtering. An empty or whitespace-only `query` SHALL behave as no filter. The chat names reported by `list_chats` are unchanged by this requirement.

#### Scenario: Accent-insensitive chat filter
- **WHEN** a group is named "Familia García" and `list_chats` is called with query "familia garcia"
- **THEN** that group is among the results

#### Scenario: Chat found through its contact's address-book name
- **WHEN** an individual chat's stored name is its bare phone number, the contact store names that JID "Maayan Levy", and `list_chats` is called with query "maayan"
- **THEN** that chat is among the results

#### Scenario: Pagination after filtering
- **WHEN** 25 chats match the query and `list_chats` is called with `limit` 20 and `page` 1
- **THEN** the remaining 5 matching chats are returned, in the requested sort order
