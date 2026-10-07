"""Shared fixtures: temporary copies of the bridge's two SQLite stores, seeded with fictitious people.

messages.db mirrors the bridge's `chats` and `messages` tables; whatsapp.db mirrors the two
whatsmeow tables the MCP server reads (`whatsmeow_contacts`, `whatsmeow_lid_map`).
"""

import sqlite3

import pytest

import whatsapp

OUR_JID = "5215500000000:17@s.whatsapp.net"

# (jid, name, last_message_time)
CHATS = [
    # Chat stored under its bare number; the address book knows it as "Rubén García"
    ("5215512345678@s.whatsapp.net", "5215512345678", "2026-10-06 10:00:00-06:00"),
    ("5215511111111@s.whatsapp.net", "Jose Ramirez", "2026-10-06 09:00:00-06:00"),
    ("5215522222222@s.whatsapp.net", "Rubén Torres", "2026-10-06 08:00:00-06:00"),
    ("5215533333333@s.whatsapp.net", "Ana López", "2026-10-06 07:00:00-06:00"),
    ("5215544444444@s.whatsapp.net", "Mariana Ruiz", "2026-10-06 06:00:00-06:00"),
    # Chat named by its number whose contact only exists in the address book as "Noa Peretz"
    ("9725550000001@s.whatsapp.net", "9725550000001", "2026-10-06 05:00:00-06:00"),
    ("120363000000000001@g.us", "Familia García", "2026-10-06 04:00:00-06:00"),
    ("status@broadcast", "status", "2026-10-06 03:00:00-06:00"),
]

# (their_jid, first_name, full_name, push_name, business_name)
CONTACTS = [
    ("5215512345678@s.whatsapp.net", "Rubén", "Rubén García", "Rubo", ""),
    # Same person again under a LID mapped to 5215512345678
    ("111111111111111@lid", "", "", "Rubén García", ""),
    ("5215522222222@s.whatsapp.net", "Rubén", "Rubén Torres", "", ""),
    # Address-book-only contacts (no chat)
    ("9725550000001@s.whatsapp.net", "Noa", "Noa Peretz", "", ""),
    ("5215566666666@s.whatsapp.net", "", "Begoña Peña", "", ""),
    # Words spread across fields: saved first name only, the surname is in the profile name
    ("5215577777777@s.whatsapp.net", "Lupita", "", "Guadalupe Hernández", ""),
    ("5215588888888@s.whatsapp.net", "", "", "", "Tacos Doña Chela"),
    # LID with no known phone-number mapping: must never surface
    ("222222222222222@lid", "", "Fantasma Sinmapa", "", ""),
]

LID_MAP = [("111111111111111", "5215512345678")]

# More than 50 address-book contacts sharing one word
BULK_COUNT = 60


def bulk_contacts():
    return [
        (f"52190000{i:05d}@s.whatsapp.net", "", f"Masivo Persona {i:02d}", "", "")
        for i in range(BULK_COUNT)
    ]


def _create_messages_db(path):
    conn = sqlite3.connect(path)
    conn.executescript(
        """
        CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
        CREATE TABLE messages (
            id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
            is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB,
            file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER,
            PRIMARY KEY (id, chat_jid)
        );
        """
    )
    conn.executemany("INSERT INTO chats VALUES (?, ?, ?)", CHATS)
    conn.executemany(
        "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES (?, ?, ?, ?, ?, ?)",
        [(f"MSG{i}", jid, jid.split("@")[0], f"hello {i}", ts, 0) for i, (jid, _, ts) in enumerate(CHATS)],
    )
    conn.commit()
    conn.close()


def _create_whatsmeow_db(path):
    conn = sqlite3.connect(path)
    conn.executescript(
        """
        CREATE TABLE whatsmeow_contacts (
            our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT, push_name TEXT,
            business_name TEXT, redacted_phone TEXT, PRIMARY KEY (our_jid, their_jid)
        );
        CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);
        """
    )
    conn.executemany(
        "INSERT INTO whatsmeow_contacts VALUES (?, ?, ?, ?, ?, ?, '')",
        [(OUR_JID, *row) for row in CONTACTS + bulk_contacts()],
    )
    conn.executemany("INSERT INTO whatsmeow_lid_map VALUES (?, ?)", LID_MAP)
    conn.commit()
    conn.close()


@pytest.fixture
def stores(tmp_path, monkeypatch):
    """Point the module at fresh temporary databases and return their paths."""
    messages_db = tmp_path / "messages.db"
    whatsmeow_db = tmp_path / "whatsapp.db"
    _create_messages_db(messages_db)
    _create_whatsmeow_db(whatsmeow_db)
    monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(messages_db))
    monkeypatch.setattr(whatsapp, "WHATSMEOW_DB_PATH", str(whatsmeow_db), raising=False)
    return {"messages": messages_db, "whatsmeow": whatsmeow_db}


def text_of(wrapped):
    """The message text inside `<<message id=…>>…<</message id=…>>` markers (None stays None)."""
    if wrapped is None:
        return None
    start = wrapped.index(">>") + 2
    return wrapped[start:wrapped.rindex("<</message id=")]
