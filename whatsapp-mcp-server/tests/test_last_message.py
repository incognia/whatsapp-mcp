import sqlite3

import pytest

from whatsapp import get_chat, get_direct_chat_by_contact, list_chats

DRIFTED = "5215591111111@s.whatsapp.net"
TIED = "5215592222222@s.whatsapp.net"
EMPTY = "5215593333333@s.whatsapp.net"


@pytest.fixture
def last_message_store(stores):
    """Add chats whose stored time drifted, whose newest messages share a second, and one with none."""
    with sqlite3.connect(stores["messages"]) as conn:
        conn.executemany(
            "INSERT INTO chats VALUES (?, ?, ?)",
            [
                # Time moved to 10:05 by an event that was never stored
                (DRIFTED, "Drifted", "2026-10-07 10:05:00-06:00"),
                (TIED, "Tied", "2026-10-07 09:00:00-06:00"),
                (EMPTY, "Empty", "2026-10-07 08:00:00-06:00"),
            ],
        )
        conn.executemany(
            "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES (?, ?, ?, ?, ?, ?)",
            [
                ("D1", DRIFTED, "5215591111111", "older", "2026-10-07 09:59:00-06:00", 0),
                ("D2", DRIFTED, "5215591111111", "see you", "2026-10-07 10:00:00-06:00", 0),
                ("T1", TIED, "5215592222222", "first in the second", "2026-10-07 09:00:00-06:00", 0),
                ("T2", TIED, "5215500000000", "second in the second", "2026-10-07 09:00:00-06:00", 1),
            ],
        )
    return stores


def by_jid(chats):
    return {c.jid: c for c in chats}


def test_drifted_chat_shows_its_newest_message(last_message_store):
    chat = by_jid(list_chats(limit=1000))[DRIFTED]
    assert chat.last_message == "see you"


def test_same_second_messages_give_one_deterministic_row(last_message_store):
    first = list_chats(limit=1000)
    second = list_chats(limit=1000)
    tied = [c for c in first if c.jid == TIED]
    assert len(tied) == 1
    # Latest inserted wins the tie, the same on every call
    assert tied[0].last_message == "second in the second"
    assert tied[0].last_is_from_me == 1
    assert by_jid(second)[TIED].last_message == tied[0].last_message


def test_chat_without_messages_is_listed_empty(last_message_store):
    chat = by_jid(list_chats(limit=1000))[EMPTY]
    assert (chat.last_message, chat.last_sender, chat.last_is_from_me) == (None, None, None)


def test_no_duplicate_chats(last_message_store):
    chats = list_chats(limit=1000)
    assert len(chats) == len({c.jid for c in chats})


def test_query_filter_keeps_newest_message(last_message_store):
    assert [(c.jid, c.last_message) for c in list_chats(query="drifted")] == [(DRIFTED, "see you")]


def test_get_chat_reports_newest_message(last_message_store):
    assert get_chat(DRIFTED).last_message == "see you"
    assert get_chat(TIED).last_message == "second in the second"
    assert get_chat(EMPTY).last_message is None


def test_get_chat_without_last_message(last_message_store):
    chat = get_chat(DRIFTED, include_last_message=False)
    assert chat.jid == DRIFTED
    assert chat.last_message is None


def test_get_chat_unknown_jid(last_message_store):
    assert get_chat("5215599999999@s.whatsapp.net") is None


def test_get_direct_chat_by_contact_reports_newest_message(last_message_store):
    assert get_direct_chat_by_contact("5215591111111").last_message == "see you"
    assert get_direct_chat_by_contact("5215592222222").last_message == "second in the second"
