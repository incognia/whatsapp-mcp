package main

import (
	"path/filepath"
	"testing"
	"time"
)

func schemaVersion(t *testing.T, store *MessageStore) int {
	t.Helper()
	var v int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateFreshDatabaseIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	if v := schemaVersion(t, store); v != len(schemaMigrations) {
		t.Fatalf("version = %d, want %d", v, len(schemaMigrations))
	}
	if err := migrate(store.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if v := schemaVersion(t, store); v != len(schemaMigrations) {
		t.Errorf("version after second migrate = %d", v)
	}
}

func TestMigrateKeepsExistingDataAndCascades(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "messages.db") + "?_foreign_keys=on"
	store, err := openMessageStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 6, 12, 0, 0, 0, time.Local)
	if err := store.StoreChat("5215512345678@s.whatsapp.net", "Alice", ts); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreMessage("M1", "5215512345678@s.whatsapp.net", "5215512345678", "hi", ts, false, "", "", "", nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	// Simulate a database from before the migration existed, then reopen it
	if _, err := store.db.Exec("DROP TABLE listener_deliveries; DROP TABLE listeners; PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
	store.Close()

	store, err = openMessageStore(dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store.Close()

	var chats, messages int
	store.db.QueryRow("SELECT COUNT(*) FROM chats").Scan(&chats)
	store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages)
	if chats != 1 || messages != 1 {
		t.Errorf("chats/messages = %d/%d, want 1/1", chats, messages)
	}

	res, err := store.db.Exec(`INSERT INTO listeners (name, webhook_url, contains) VALUES ('x', 'http://127.0.0.1:9/h', '["a"]')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := store.db.Exec(`INSERT INTO listener_deliveries (listener_id, delivery_id, event, status) VALUES (?, 'd1', 'message', 'delivered')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM listeners WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	var deliveries int
	store.db.QueryRow("SELECT COUNT(*) FROM listener_deliveries").Scan(&deliveries)
	if deliveries != 0 {
		t.Errorf("deliveries after deleting the listener = %d, want 0", deliveries)
	}
}
