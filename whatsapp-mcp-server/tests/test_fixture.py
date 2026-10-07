import sqlite3


def test_fixture_builds_both_stores(stores):
    with sqlite3.connect(stores["messages"]) as conn:
        assert conn.execute("SELECT COUNT(*) FROM chats").fetchone()[0] == 8
    with sqlite3.connect(stores["whatsmeow"]) as conn:
        assert conn.execute("SELECT COUNT(*) FROM whatsmeow_contacts").fetchone()[0] == 68
