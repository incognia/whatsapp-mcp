package main

import (
	"path/filepath"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

const ownUser = "5215500000000"

// openTestStore opens a message store in a temporary file. A file is used instead of
// ":memory:" because database/sql pools connections and each in-memory connection
// would see its own empty database.
func openTestStore(t *testing.T) *MessageStore {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "messages.db") + "?_foreign_keys=on"
	store, err := openMessageStore(dsn)
	if err != nil {
		t.Fatalf("openMessageStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

type storedRow struct {
	sender, content, mediaType, filename, url string
	isFromMe                                  bool
	mediaKey, fileSHA256, fileEncSHA256       []byte
	fileLength                                uint64
	timestamp                                 time.Time
}

func readRow(t *testing.T, store *MessageStore, msgID, chatJID string) storedRow {
	t.Helper()
	var r storedRow
	err := store.db.QueryRow(`SELECT sender, content, media_type, filename, url, is_from_me,
		media_key, file_sha256, file_enc_sha256, file_length, timestamp
		FROM messages WHERE id = ? AND chat_jid = ?`, msgID, chatJID).Scan(
		&r.sender, &r.content, &r.mediaType, &r.filename, &r.url, &r.isFromMe,
		&r.mediaKey, &r.fileSHA256, &r.fileEncSHA256, &r.fileLength, &r.timestamp)
	if err != nil {
		t.Fatalf("reading message %s in %s: %v", msgID, chatJID, err)
	}
	return r
}

func readChat(t *testing.T, store *MessageStore, chatJID string) (string, time.Time) {
	t.Helper()
	var name string
	var last time.Time
	if err := store.db.QueryRow("SELECT name, last_message_time FROM chats WHERE jid = ?", chatJID).Scan(&name, &last); err != nil {
		t.Fatalf("reading chat %s: %v", chatJID, err)
	}
	return name, last
}

func countRows(t *testing.T, store *MessageStore, msgID, chatJID string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = ? AND chat_jid = ?", msgID, chatJID).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	return n
}

func TestStoreSentMessageKinds(t *testing.T) {
	ts := time.Date(2026, 10, 6, 17, 30, 15, 0, time.Local)
	direct := types.NewJID("5215512345678", types.DefaultUserServer)
	group := types.NewJID("120363000000000011", types.GroupServer)

	cases := []struct {
		name          string
		chat          types.JID
		msgID         string
		msg           *waProto.Message
		wantContent   string
		wantMediaType string
		wantFilename  string
		wantMedia     bool
	}{
		{
			name:        "plain text",
			chat:        direct,
			msgID:       "3EB0AAAAAAAA0001",
			msg:         &waProto.Message{Conversation: ptr("hello")},
			wantContent: "hello",
		},
		{
			name:  "text with mentions",
			chat:  group,
			msgID: "3EB0AAAAAAAA0002",
			msg: &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text:        ptr("@5215500000012 take a look"),
				ContextInfo: &waProto.ContextInfo{MentionedJID: []string{"5215500000012@s.whatsapp.net"}},
			}},
			wantContent: "@5215500000012 take a look",
		},
		{
			name:  "image with caption",
			chat:  direct,
			msgID: "3EB0AAAAAAAA0003",
			msg: &waProto.Message{ImageMessage: &waProto.ImageMessage{
				Caption:       ptr("the photo"),
				URL:           ptr("https://mmg.whatsapp.net/v/t62.7118-24/1_n.enc?ccb=11-4&oh=x&oe=y"),
				MediaKey:      []byte{1, 2, 3},
				FileSHA256:    []byte{4, 5, 6},
				FileEncSHA256: []byte{7, 8, 9},
				FileLength:    ptr(uint64(42272)),
			}},
			wantContent:   "the photo",
			wantMediaType: "image",
			wantFilename:  "image_20261006_173015_AAAA0003.jpg",
			wantMedia:     true,
		},
		{
			name:  "document without caption",
			chat:  direct,
			msgID: "3EB0AAAAAAAA0004",
			msg: &waProto.Message{DocumentMessage: &waProto.DocumentMessage{
				Title:      ptr("report.pdf"),
				URL:        ptr("https://mmg.whatsapp.net/v/t62.7119-24/2_n.enc?ccb=11-4"),
				MediaKey:   []byte{1},
				FileLength: ptr(uint64(1024)),
			}},
			wantContent:   "",
			wantMediaType: "document",
			wantFilename:  "document_20261006_173015_AAAA0004",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			if err := storeSentMessage(store, tc.chat, "Some chat", ownUser, tc.msgID, ts, tc.msg); err != nil {
				t.Fatalf("storeSentMessage: %v", err)
			}

			row := readRow(t, store, tc.msgID, tc.chat.String())
			if !row.isFromMe || row.sender != ownUser {
				t.Errorf("is_from_me = %v, sender = %q; want true, %q", row.isFromMe, row.sender, ownUser)
			}
			if row.content != tc.wantContent {
				t.Errorf("content = %q, want %q", row.content, tc.wantContent)
			}
			if row.mediaType != tc.wantMediaType || row.filename != tc.wantFilename {
				t.Errorf("media = %q/%q, want %q/%q", row.mediaType, row.filename, tc.wantMediaType, tc.wantFilename)
			}
			if tc.wantMedia && (row.url == "" || len(row.mediaKey) == 0 || len(row.fileSHA256) == 0 ||
				len(row.fileEncSHA256) == 0 || row.fileLength == 0) {
				t.Errorf("media metadata not stored: %+v", row)
			}

			_, last := readChat(t, store, tc.chat.String())
			if !last.Equal(row.timestamp) {
				t.Errorf("chat last_message_time %v != message timestamp %v", last, row.timestamp)
			}
		})
	}
}

func TestStoreSentMessageChatBookkeeping(t *testing.T) {
	store := openTestStore(t)
	chat := types.NewJID("5215512345678", types.DefaultUserServer)
	older := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	sent := time.Date(2026, 10, 6, 17, 30, 15, 0, time.Local)

	// A brand-new chat is created before the message, so the foreign key is satisfied
	fresh := types.NewJID("5215599999999", types.DefaultUserServer)
	if err := storeSentMessage(store, fresh, "5215599999999", ownUser, "3EB0NEWCHAT", sent, &waProto.Message{Conversation: ptr("hi")}); err != nil {
		t.Fatalf("first message to a new chat: %v", err)
	}

	// An existing chat keeps its name and moves to the sent message's time
	if err := store.StoreChat(chat.String(), "Alice", older); err != nil {
		t.Fatalf("seeding chat: %v", err)
	}
	if err := storeSentMessage(store, chat, "Alice", ownUser, "3EB0EXISTING", sent, &waProto.Message{Conversation: ptr("hey")}); err != nil {
		t.Fatalf("storeSentMessage: %v", err)
	}
	name, last := readChat(t, store, chat.String())
	if name != "Alice" {
		t.Errorf("chat name = %q, want %q", name, "Alice")
	}
	if !last.Equal(sent) {
		t.Errorf("last_message_time = %v, want %v", last, sent)
	}
}

func TestStoreSentMessageIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	chat := types.NewJID("5215512345678", types.DefaultUserServer)
	ts := time.Date(2026, 10, 6, 17, 30, 15, 0, time.Local)
	msg := &waProto.Message{Conversation: ptr("only once")}

	for i := 0; i < 2; i++ {
		if err := storeSentMessage(store, chat, "Alice", ownUser, "3EB0DUPLICATE", ts, msg); err != nil {
			t.Fatalf("storeSentMessage #%d: %v", i+1, err)
		}
	}

	// An echo of the same message arriving later as an event, stored the way handleMessage does
	if err := store.StoreMessage("3EB0DUPLICATE", chat.String(), ownUser, "only once", ts, true, "", "", "", nil, nil, nil, 0); err != nil {
		t.Fatalf("echo: %v", err)
	}

	if n := countRows(t, store, "3EB0DUPLICATE", chat.String()); n != 1 {
		t.Errorf("rows for the message = %d, want 1", n)
	}
	if row := readRow(t, store, "3EB0DUPLICATE", chat.String()); !row.isFromMe {
		t.Errorf("is_from_me = false after echo, want true")
	}
}
