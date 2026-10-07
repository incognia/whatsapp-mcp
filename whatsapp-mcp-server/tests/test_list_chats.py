import sqlite3

from conftest import text_of
from whatsapp import list_chats


def jids(chats):
    return [c.jid for c in chats]


def add_chats(stores, count, name_prefix):
    with sqlite3.connect(stores["messages"]) as conn:
        conn.executemany(
            "INSERT INTO chats VALUES (?, ?, ?)",
            [
                (f"5215700{i:06d}@s.whatsapp.net", f"{name_prefix} {i:02d}", f"2026-09-{(i % 28) + 1:02d} 12:00:00-06:00")
                for i in range(count)
            ],
        )


def test_accent_insensitive_chat_filter(stores):
    assert "120363000000000001@g.us" in jids(list_chats(query="familia garcia"))


def test_chat_found_through_address_book_name(stores):
    # Stored as its bare number; only the address book knows it as "Noa Peretz"
    chats = list_chats(query="noa")
    assert jids(chats) == ["9725550000001@s.whatsapp.net"]
    # The reported name is unchanged
    assert chats[0].name == "9725550000001"


def test_filter_matches_jid(stores):
    assert jids(list_chats(query="5215511111111")) == ["5215511111111@s.whatsapp.net"]


def test_pagination_after_filtering(stores):
    add_chats(stores, 25, "Equipo Núñez")
    first = list_chats(query="equipo nunez", limit=20, page=0)
    second = list_chats(query="equipo nunez", limit=20, page=1)
    assert len(first) == 20
    assert len(second) == 5
    assert not set(jids(first)) & set(jids(second))
    # Same order as the unpaginated, sorted listing
    times = [c.last_message_time for c in first + second]
    assert times == sorted(times, reverse=True)


def test_sort_by_name_with_filter(stores):
    chats = list_chats(query="ruben", sort_by="name")
    assert [c.name for c in chats] == sorted(c.name for c in chats)
    assert "Rubén Torres" in [c.name for c in chats]


def test_include_last_message_false(stores):
    chats = list_chats(query="jose", include_last_message=False)
    assert jids(chats) == ["5215511111111@s.whatsapp.net"]
    assert chats[0].last_message is None


def test_last_message_included_by_default(stores):
    chat = list_chats(query="jose")[0]
    assert text_of(chat.last_message) == "hello 1"


def test_blank_query_is_no_filter(stores):
    assert jids(list_chats(query="   ", limit=100)) == jids(list_chats(limit=100))
    assert len(list_chats(limit=100)) == 8


def test_unfiltered_pagination(stores):
    assert len(list_chats(limit=3, page=0)) == 3
    assert len(list_chats(limit=3, page=2)) == 2


def test_no_match_returns_empty(stores):
    assert list_chats(query="zzzz") == []
