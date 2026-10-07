import sqlite3

import pytest

import whatsapp


def test_missing_database_is_not_created(tmp_path, monkeypatch):
    missing = tmp_path / "whatsapp.db"
    monkeypatch.setattr(whatsapp, "WHATSMEOW_DB_PATH", str(missing))

    with pytest.raises(sqlite3.OperationalError):
        whatsapp._connect_whatsmeow_db()
    assert not missing.exists()


def test_connection_is_read_only(stores):
    conn = whatsapp._connect_whatsmeow_db()
    try:
        with pytest.raises(sqlite3.OperationalError):
            conn.execute("DELETE FROM whatsmeow_contacts")
    finally:
        conn.close()


def test_lid_rows_map_to_phone_numbers_and_merge(stores):
    book = whatsapp._load_address_book()

    entry = book["5215512345678@s.whatsapp.net"]
    # The phone-number row's names win; the LID row adds nothing new here
    assert entry.full_name == "Rubén García"
    assert entry.push_name == "Rubo"
    assert not any(jid.endswith("@lid") for jid in book)


def test_unmapped_lid_is_omitted(stores):
    book = whatsapp._load_address_book()
    assert all(entry.full_name != "Fantasma Sinmapa" for entry in book.values())


def test_missing_database_falls_back_to_empty(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(whatsapp, "WHATSMEOW_DB_PATH", str(tmp_path / "nope.db"))
    assert whatsapp._load_address_book() == {}
    assert "searching chats only" in capsys.readouterr().out


def test_locked_database_falls_back_to_empty(stores, monkeypatch):
    def locked():
        raise sqlite3.OperationalError("database is locked")

    monkeypatch.setattr(whatsapp, "_connect_whatsmeow_db", locked)
    assert whatsapp._load_address_book() == {}
