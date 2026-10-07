## Context

See proposal.md (Why) for the motivation and specs/contact-search/spec.md for the behaviour contract.

Current state relevant to the approach:

- `whatsapp-mcp-server/whatsapp.py` opens `messages.db` (path in `MESSAGES_DB_PATH`) with a plain `sqlite3.connect` per call. `search_contacts` runs `LOWER(name) LIKE LOWER('%query%') OR LOWER(jid) LIKE ...` over `chats`, excludes `@g.us` and returns at most 50 `Contact(phone_number, name, jid)` rows ordered by name. `list_chats` applies a similar `LIKE` filter in SQL, then `LIMIT/OFFSET`. SQLite's `LOWER` and `LIKE` only fold ASCII case and know nothing about diacritics.
- The Go bridge translates LID JIDs to phone-number JIDs before storing chats and senders, and migrates old LID chats on startup, so `messages.db` chats are keyed by `<number>@s.whatsapp.net` (the local store has no `@lid` chats). Individual chats without a known name store the bare number as their name (about 294 of 544 in the local store).
- whatsmeow's `whatsapp.db` (owned and written by the bridge, rollback-journal mode) has `whatsmeow_contacts(our_jid, their_jid, first_name, full_name, push_name, business_name, redacted_phone)` and `whatsmeow_lid_map(lid, pn)` with bare user parts (no server suffix). Locally there are about 1,600 contact rows for a single `our_jid`; a handful are keyed by `@lid`, each with a mapping and a matching phone-number row, so LID rows are duplicates of people that also appear by number.
- The MCP server has no tests; Python dependencies are managed with uv in `whatsapp-mcp-server/pyproject.toml` (Python ≥ 3.11).
- Reference: `LukasHaas/whatsapp-mcp` (adopted upstream in PR #343) adds `normalize_text` (NFKD, drop marks, `lower()`), `_get_whatsmeow_contacts`, `_get_lid_map`, merges the address book into `search_contacts` and filters `list_chats` in Python. This design reuses its ideas but differs where noted below.

## Goals / Non-Goals

**Goals:**
- One shared, pure normalisation and matching routine used by both `search_contacts` and the `list_chats` filter, so both tools behave identically.
- A single read-only load of the address book per call that already resolves LIDs and merges duplicates, keyed by phone-number JID.
- Keep the tool signatures and result shapes unchanged; keep the cost per call in the low milliseconds for address books of a few thousand entries.
- A pytest suite that runs without the bridge, a phone or real data.

**Non-Goals:**
- Changing the names that `list_chats`, `get_chat`, `get_sender_name` or `resolve_mentions` display (they keep using `chats.name`); enriching them from the address book is a possible follow-up.
- Fuzzy or typo-tolerant matching (edit distance, phonetics); only diacritics, case and word order are forgiven.
- Accent-insensitive search of message content (`list_messages`'s `query`).
- Any change to the Go bridge, its schema or the REST API, and any write to `whatsapp.db`.
- Adding new tool parameters (for example a configurable result limit).

## Decisions

### D1. Normalise in Python with `unicodedata`, not in SQLite

`normalise_text(text)` applies `unicodedata.normalize("NFKD", text)`, drops every code point for which `unicodedata.combining(c)` is non-zero, then `casefold()`s and collapses runs of whitespace. Both the query and every candidate string go through it. "Rubén" → "ruben", "Begoña" → "begona", "Straße" → "strasse", full-width and ligature forms are folded by NFKD's compatibility mapping.

`casefold()` is used instead of LukasHaas's `lower()` because it is the Unicode-correct caseless comparison (for example ß/ss, final sigma).

Alternatives considered:
- *SQLite `LOWER`/`LIKE`/`COLLATE NOCASE`*: ASCII-only; cannot ignore accents. Rejected.
- *ICU extension (`icu_load_collation`)*: not bundled with Python's `sqlite3` on macOS; adds a native dependency. Rejected.
- *FTS5 with `unicode61 remove_diacritics 2`*: needs a virtual table and index, which means writing either to the bridge's databases (forbidden) or building an in-memory index on every call; its token-prefix semantics also differ from the substring matching users get today. Rejected as overkill for a few thousand rows.
- *`conn.create_function("normalise", ...)` and filtering in SQL*: workable, but the merge spans two databases and the per-row Python callback costs the same as filtering in Python, with less readable code. Rejected.
- *Normalised columns maintained by the bridge*: requires Go changes and a schema migration, and the address book lives in whatsmeow's tables, which we do not own. Rejected.

### D2. Open `whatsapp.db` read-only through a URI, on its own short-lived connection

Add `WHATSMEOW_DB_PATH` next to `MESSAGES_DB_PATH` and a helper that connects with `sqlite3.connect(Path(WHATSMEOW_DB_PATH).resolve().as_uri() + "?mode=ro", uri=True, timeout=2)`. `as_uri()` percent-encodes spaces and other special characters in the path. `mode=ro` guarantees no writes and, unlike a plain `connect`, does not create an empty database when the file is missing (it raises `sqlite3.OperationalError` instead). The helper reads the two tables with plain `SELECT`s, materialises the rows and closes the connection immediately, so the shared lock is held for milliseconds and the bridge's writers are not starved.

Any `sqlite3.Error` (missing file, `SQLITE_BUSY` after the timeout, a future whatsmeow schema change) is caught and logged once per call, and the address book is treated as empty, so `search_contacts` degrades to chats-only behaviour as the spec requires.

Alternatives considered:
- *`immutable=1`*: skips locking entirely but can return torn or stale data while the bridge writes. Rejected.
- *`ATTACH` the database to the `messages.db` connection*: would need `uri=True` on the main connection and a single cross-database query; it couples the two stores and makes the fallback harder. Rejected.
- *LukasHaas's plain `sqlite3.connect`, opened twice (contacts and LID map)*: read-write mode and an extra connection. Rejected.

### D3. Build one merged index keyed by phone-number JID

`_load_address_book()` returns `dict[pn_jid, AddressBookEntry]`, where an entry holds the individual name fields (`full_name`, `first_name`, `business_name`, `push_name`). It reads `whatsmeow_lid_map` into `lid → pn`, then iterates `whatsmeow_contacts`:

- `…@s.whatsapp.net` rows are used as-is; `…@lid` rows are translated through the map and **skipped when unmapped**; other servers are ignored. `our_jid` is ignored (one linked device in practice; if several exist, their rows simply merge).
- When several rows land on the same phone-number JID, each field keeps the first non-empty value seen, preferring the phone-number row over a LID row.

`search_contacts` then reads the individual chats from `messages.db` (`jid LIKE '%@s.whatsapp.net'`, which also excludes groups, broadcasts, newsletters and status) and builds candidates as the union of chat JIDs and address-book JIDs. Each candidate carries: its display name chosen per the spec's precedence (full name → chat name unless it equals the bare number → first name → business name → push name → number), every non-empty name as a searchable field, its phone digits, and a `has_chat` flag.

Differences from LukasHaas: there, LID entries stay in the result set as separate `@lid` contacts (duplicates of the phone-number entry), only the single "best" name is searchable (a push name such as "Rubo" cannot be found once a full name exists), and the chat name loses to any whatsmeow name. Here, LIDs never leak into results, every name is searchable and the user's own saved name wins.

### D4. Matching and ranking

`_query_words(query)` returns `normalise_text(query).split()`. If the raw query matches `^[\d\s+\-().]+$` and contains a digit, it is treated as a phone query: a single word made of its digits, matched against the phone digits only.

A candidate matches when every word is a substring of its haystack, which is the normalised searchable fields plus the phone digits (and, for `list_chats`, the lower-cased JID) joined by spaces. Words never contain whitespace, so a word cannot straddle two fields, while different words may hit different fields, as the spec requires.

Ranking key: `(0 if every word starts some word of the haystack else 1, 0 if has_chat else 1, normalise_text(display_name), jid)`; the list is sorted and cut to 50. The word-start tier keeps "ana" → "Ana López" ahead of "Mariana Ruiz"; preferring contacts with a chat favours people the user actually talks to. A blank query returns `[]` before touching either database.

### D5. `list_chats` filters in Python only when a query is given

Without a (non-blank) query, `list_chats` keeps its current SQL path with `LIMIT/OFFSET`. With a query, it runs the same `SELECT … ORDER BY …` without `LIMIT/OFFSET`, loads the address book once, keeps rows whose haystack (chat name, lower-cased JID and, for `@s.whatsapp.net` chats, that JID's address-book names) matches all words, then slices `[page*limit : (page+1)*limit]`. Sorting stays in SQL, so `sort_by` keeps its meaning; displayed names are not changed (non-goal). With about 600 chats this costs a few milliseconds.

### D6. No caching

Loading about 1,600 contact rows and 600 chats and normalising them takes a few milliseconds per call, negligible next to an MCP round trip. A cache would have to track whatsmeow's writes to stay correct. If the address book grows by orders of magnitude, a cache keyed by the file's `mtime` can be added behind `_load_address_book()` without changing behaviour.

### D7. Code layout and tests

All new helpers live in `whatsapp.py` (private, prefixed with `_`, except `normalise_text`) to match the single-module structure of the server; the `Contact` and `Chat` dataclasses are unchanged. Database paths are read from the module-level constants at call time so tests can monkeypatch them.

Tests go in `whatsapp-mcp-server/tests/` with pytest added via `uv add --dev pytest` and `[tool.pytest.ini_options] pythonpath = ["."]`. A `conftest.py` fixture creates temporary `messages.db` (`chats`, `messages`) and `whatsapp.db` (`whatsmeow_contacts`, `whatsmeow_lid_map`) with the minimal columns used, seeded with fictitious names, and monkeypatches `MESSAGES_DB_PATH` and `WHATSMEOW_DB_PATH`. One test checks the contact-store file's bytes and modification time are unchanged after searching, and one that a missing `whatsapp.db` is not created.

## Risks / Trade-offs

- [Letters that NFKD does not decompose (ø, ł, đ, æ) still need an exact match] → Acceptable for Spanish, where all accented letters decompose; a small explicit translation table can be added to `normalise_text` later without changing the spec.
- [Substring matching of short queries ("an") returns many hits] → Word-start ranking plus the 50-result cap keeps the best matches first; behaviour is no broader than today's `LIKE '%an%'`.
- [`whatsmeow_contacts` also holds push names of non-saved people seen in groups, so results grow beyond the saved address book] → Useful for finding group participants; such entries rank after word-start matches with chats, and the reported name tells the assistant who they are.
- [A future whatsmeow schema change breaks the `SELECT`] → Caught as `sqlite3.Error`; search falls back to chats only, and the log line points at the cause.
- [Reading while the bridge writes in rollback-journal mode] → Read-only connection, 2-second busy timeout, rows materialised and connection closed at once; on persistent `SQLITE_BUSY`, fall back to chats only.
- [Unmapped LID contacts are omitted] → None exist in the local store today; returning them would hand the assistant a JID that the rest of the server does not use. Revisit if the bridge's LID mapping proves incomplete.
- [Results now include people without chats, which changes what the assistant sees] → Intended; the data is already on the same machine and the tool contract is unchanged.

## Migration Plan

No data migration. Deploy by updating the MCP server code and running `uv sync` (pulls the pytest dev dependency only for development). The bridge does not need restarting. Rollback is a revert of the `whatsapp.py`/`main.py` changes; nothing persistent is created.

## Open Questions

- Whether a later change should use the merged address book to improve the names shown by `list_chats`, `get_chat`, `get_sender_name` and `resolve_mentions` for chats stored under a bare number. Deferred; it does not affect this change's specs or tasks.
