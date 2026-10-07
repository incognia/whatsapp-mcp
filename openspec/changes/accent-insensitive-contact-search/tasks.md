## 1. Test scaffolding

- [x] 1.1 In `whatsapp-mcp-server/`, run `uv add --dev pytest` and add `[tool.pytest.ini_options]` with `pythonpath = ["."]` and `testpaths = ["tests"]` to `pyproject.toml`; verify `uv run pytest --version` succeeds and `uv.lock` is updated
- [x] 1.2 Create `whatsapp-mcp-server/tests/conftest.py` with a fixture that builds temporary `messages.db` (`chats`, `messages`) and `whatsapp.db` (`whatsmeow_contacts`, `whatsmeow_lid_map`) with the bridge's column names, seeds fictitious contacts covering every spec scenario (accented names, a chat named by its bare number, an address-book-only contact, a mapped and an unmapped `@lid` contact, a group, more than 50 matches for one query), and monkeypatches `whatsapp.MESSAGES_DB_PATH` and `whatsapp.WHATSMEOW_DB_PATH`; verify `uv run pytest` collects the fixture without errors

## 2. Normalisation and matching helpers (`whatsapp.py`)

- [x] 2.1 Add `WHATSMEOW_DB_PATH` (next to `MESSAGES_DB_PATH`) and `normalise_text(text)` (NFKD, drop combining marks, `casefold()`, collapse whitespace; `None`/empty → `""`) per design D1; verify with `tests/test_normalise.py` cases "Rubén García" → "ruben garcia", "Begoña" → "begona", "MAAYAN" → "maayan", "Straße" → "strasse"
- [x] 2.2 Add `_query_words(query)` (normalised words; phone-like queries collapsed to one digits-only word) and `_match_rank(words, haystack)` returning `None` for no match, `0` when every word starts a word of the haystack and `1` for inside-word matches, per design D4; verify unit tests for any word order, partial words, all-words-required, "+52 1 55 1234 5678" → "5215512345678", and blank query → no words

## 3. Read-only address book (`whatsapp.py`)

- [x] 3.1 Add `_connect_whatsmeow_db()` opening `Path(WHATSMEOW_DB_PATH).resolve().as_uri() + "?mode=ro"` with `uri=True, timeout=2` per design D2; verify a test that a missing `whatsapp.db` raises `sqlite3.OperationalError` and is not created on disk
- [x] 3.2 Add an `AddressBookEntry` holder and `_load_address_book()` that reads `whatsmeow_lid_map` and `whatsmeow_contacts` in one short-lived connection, maps `@lid` rows to `<pn>@s.whatsapp.net`, skips unmapped LIDs and non-user servers, merges duplicates preferring the phone-number row, and returns `{}` (with one logged message) on any `sqlite3.Error`, per design D3; verify tests for LID mapping, unmapped-LID omission, duplicate merging and the missing/locked-file fallback

## 4. `search_contacts`

- [x] 4.1 Rewrite `search_contacts(query)` in `whatsapp.py`: return `[]` for a blank query; load individual chats (`jid LIKE '%@s.whatsapp.net'`) from `messages.db` and the address book; build one candidate per phone-number JID with the spec's display-name precedence, all names searchable and a `has_chat` flag; filter with `_match_rank`; sort by `(rank, not has_chat, normalised name, jid)`; return the first 50 as unchanged `Contact` objects; verify `tests/test_search_contacts.py` covers every scenario of the "Accent- and case-insensitive name matching", "Multi-word queries match in any order", "Phone number matching", "Address-book contacts are searchable", "LID-keyed contacts resolve to phone numbers", "One result per contact", "Groups excluded from contact search" and "Result shape, limit and ordering" requirements
- [x] 4.2 Add a test that `whatsapp.db`'s bytes and modification time are unchanged after repeated `search_contacts` calls; verify it passes
- [x] 4.3 Update the `search_contacts` tool docstring in `main.py` to say it searches chats and the address book, ignores accents and case, and matches words in any order; verify `uv run python -c "import main"` succeeds

## 5. `list_chats` name filter

- [x] 5.1 Change `list_chats` in `whatsapp.py` so a non-blank `query` runs the existing `SELECT … ORDER BY …` without `LIMIT/OFFSET`, filters rows in Python on chat name, lower-cased JID and (for `@s.whatsapp.net` chats) address-book names using `_query_words`/`_match_rank`, then slices by `page`/`limit`; a blank `query` keeps the current SQL path; reported names stay unchanged, per design D5; verify `tests/test_list_chats.py` covers the "Chat name filter uses the same matching" scenarios, `sort_by="name"`, `include_last_message=False` and the unfiltered path
- [x] 5.2 Update the `list_chats` tool docstring in `main.py` for the new `query` semantics; verify `uv run python -c "import main"` succeeds

## 6. End-to-end check and documentation

- [x] 6.1 Run `uv run pytest` in `whatsapp-mcp-server/` and verify all tests pass
- [x] 6.2 With the bridge running against the real store, call the `search_contacts` MCP tool with an unaccented, reversed-order query for a known accented contact and with the name of an address-book-only contact, and `list_chats` with an unaccented query; verify the expected contacts and chats are returned, no `@lid` JIDs appear, and the bridge logs no database errors
- [x] 6.3 Before committing, add a `CHANGELOG.md` entry in UK English under `## [YYYY-MM-DD] - <Title>` using the current date in CST (UTC-6), at the top in reverse chronological order, with typed bullets (`feat:` for address-book search, accent-insensitive any-order matching and the `list_chats` filter; `chore:` for the pytest suite), crediting the approach from the LukasHaas fork (upstream PR #343); verify the entry renders under the newest date heading
