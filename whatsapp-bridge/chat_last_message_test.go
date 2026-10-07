package main

import (
	"database/sql"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func chatTime(t *testing.T, store *MessageStore, jid string) (string, sql.NullTime) {
	t.Helper()
	var name string
	var last sql.NullTime
	if err := store.db.QueryRow("SELECT name, last_message_time FROM chats WHERE jid = ?", jid).Scan(&name, &last); err != nil {
		t.Fatalf("reading chat %s: %v", jid, err)
	}
	return name, last
}

func TestEnsureChatCreatesMissingChat(t *testing.T) {
	store := openTestStore(t)
	if err := store.EnsureChat("5215591111111@s.whatsapp.net", "New person"); err != nil {
		t.Fatalf("EnsureChat: %v", err)
	}
	name, last := chatTime(t, store, "5215591111111@s.whatsapp.net")
	if name != "New person" || last.Valid {
		t.Errorf("chat = %q / %v, want %q with no time", name, last, "New person")
	}
}

func TestEnsureChatLeavesExistingChatUntouched(t *testing.T) {
	store := openTestStore(t)
	jid := "5215591111111@s.whatsapp.net"
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	if err := store.StoreChat(jid, "Alice", at); err != nil {
		t.Fatalf("StoreChat: %v", err)
	}
	if err := store.EnsureChat(jid, "Someone else"); err != nil {
		t.Fatalf("EnsureChat: %v", err)
	}
	name, last := chatTime(t, store, jid)
	if name != "Alice" || !last.Time.Equal(at) {
		t.Errorf("chat = %q / %v, want %q / %v", name, last.Time, "Alice", at)
	}
}

func TestRepairChatLastMessageTimes(t *testing.T) {
	store := openTestStore(t)
	logger := waLog.Noop
	drifted := "5215591111111@s.whatsapp.net"
	empty := "5215593333333@s.whatsapp.net"
	newest := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	ahead := newest.Add(5 * time.Minute)
	emptyTime := time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)

	// A chat whose time was moved ahead of its newest stored message
	if err := store.StoreChat(drifted, "Drifted", newest.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i, ts := range []time.Time{newest.Add(-time.Minute), newest} {
		id := []string{"M1", "M2"}[i]
		if err := store.StoreMessage(id, drifted, "5215591111111", "hi", ts, false, "", "", "", nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.StoreChat(drifted, "Drifted", ahead); err != nil {
		t.Fatal(err)
	}
	// A chat with no stored messages
	if err := store.StoreChat(empty, "Empty", emptyTime); err != nil {
		t.Fatal(err)
	}

	if err := repairChatLastMessageTimes(store, logger); err != nil {
		t.Fatalf("repair: %v", err)
	}

	name, last := chatTime(t, store, drifted)
	if name != "Drifted" || !last.Time.Equal(newest) {
		t.Errorf("drifted chat = %q / %v, want %q / %v", name, last.Time, "Drifted", newest)
	}
	if name, last := chatTime(t, store, empty); name != "Empty" || !last.Time.Equal(emptyTime) {
		t.Errorf("empty chat = %q / %v, want unchanged %v", name, last.Time, emptyTime)
	}
	var messages int
	store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages)
	if messages != 2 {
		t.Errorf("messages = %d, want 2 (untouched)", messages)
	}

	// A second run is a no-op
	if err := repairChatLastMessageTimes(store, logger); err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if _, again := chatTime(t, store, drifted); !again.Time.Equal(newest) {
		t.Errorf("second run moved the chat to %v", again.Time)
	}
	if _, again := chatTime(t, store, empty); !again.Time.Equal(emptyTime) {
		t.Errorf("second run moved the empty chat to %v", again.Time)
	}
}
