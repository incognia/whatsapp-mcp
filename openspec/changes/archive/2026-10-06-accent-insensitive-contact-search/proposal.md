## Why

The `search_contacts` MCP tool only runs a case-insensitive SQL `LIKE '%query%'` over the `chats` table in `messages.db`, so it misses most real contacts: "ruben" does not find "Rubén García", "garcia ruben" finds nothing because word order matters, and anyone in the phone's address book without message history is invisible. On the current store, roughly 294 of the 544 direct chats carry only a bare phone number as their name, while whatsmeow's own `whatsmeow_contacts` table in `whatsapp.db` holds about 1,600 named contacts that the MCP server never reads. The user writes Spanish names without accents, so today the assistant regularly fails to find the person it has been asked to message.

## What Changes

- `search_contacts` searches a merged contact set: the non-group chats in `messages.db` plus the full address book in whatsmeow's `whatsmeow_contacts` (`full_name`, `first_name`, `push_name`, `business_name`), opened strictly read-only.
- Matching becomes accent-insensitive and case-insensitive (Unicode normalisation that strips diacritics and case-folds both the query and the candidate names), so "ruben" matches "Rubén" and "NOA" matches "Noa".
- Multi-word queries match when every word appears somewhere in the contact's names or phone number, in any order ("garcia ruben" finds "Rubén García").
- Address-book entries keyed by LID JIDs (`@lid`) are translated to their phone-number JID through `whatsmeow_lid_map`, so results use the same phone-number JIDs as the chats stored by the bridge, and one person appears once, not once per JID or per source.
- Results stay limited to 50, are ordered deterministically (better matches first, then by name), and keep the existing `phone_number`, `name` and `jid` fields, so the tool contract stays backward compatible.
- The `query` filter of `list_chats` uses the same accent-insensitive, any-order word matching, and also matches a direct chat by its contact's address-book name, while keeping its pagination and sort options.
- A small pytest suite covers normalisation, matching and the merge logic against temporary SQLite fixtures, and `pytest` is added as a development dependency.
- No changes to the Go bridge, its database schema or the REST API.

## Capabilities

### New Capabilities
- `contact-search`: how the MCP server finds contacts and chats by name or phone number: the sources it searches (chats and the whatsmeow address book), accent- and case-insensitive multi-word matching, LID-to-phone-number resolution, de-duplication, ordering and result limits for `search_contacts`, and the matching rules for the `list_chats` name filter.

### Modified Capabilities
<!-- None: openspec/specs/ has no existing capabilities yet. -->

## Impact

- **Code**: `whatsapp-mcp-server/whatsapp.py` (`search_contacts`, `list_chats`, new helpers for text normalisation, read-only access to `whatsapp.db`, address-book loading and LID mapping, plus a `WHATSMEOW_DB_PATH` constant); `whatsapp-mcp-server/main.py` (tool docstrings for `search_contacts` and `list_chats`).
- **Data**: reads `whatsapp-bridge/store/whatsapp.db`, owned and written by whatsmeow, via a read-only SQLite URI; nothing is ever written to it. If that file is missing or locked, the search falls back to chats only.
- **APIs**: the `search_contacts` and `list_chats` tool signatures and result shapes are unchanged; results become broader (address-book contacts without history) and more tolerant (accents, case, word order).
- **Dependencies**: no new runtime dependencies (standard-library `unicodedata`); `pytest` is added as a uv development dependency.
- **Docs**: `CHANGELOG.md` entry.
- **Credit**: approach adapted from the `LukasHaas/whatsapp-mcp` fork (adopted upstream in PR #343), which introduced address-book merging and accent-insensitive multi-word contact search.
