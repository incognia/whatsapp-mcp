package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"

	"bytes"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Message represents a chat message for our client
type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

// Database handler for storing message history
type MessageStore struct {
	db *sql.DB
}

// Initialize message store
func NewMessageStore() (*MessageStore, error) {
	// Create directory for database if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %v", err)
	}

	return openMessageStore("file:store/messages.db?_foreign_keys=on")
}

// openMessageStore opens the message database at dsn and creates its tables if needed
func openMessageStore(dsn string) (*MessageStore, error) {
	// Open SQLite database for messages
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Create tables if they don't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);
		
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// Versioned, additive schema steps on top of the base tables
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &MessageStore{db: db}, nil
}

// Close the database connection
func (store *MessageStore) Close() error {
	return store.db.Close()
}

// StoreChat records a chat and its last message time. It never moves an existing chat's time
// backwards (older history batches can arrive after newer messages) and never replaces a
// stored name with an empty one. Times are compared in Go: they are stored as text with a UTC
// offset, which SQL comparison would get wrong across offsets.
func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	var existingName sql.NullString
	var existingTime sql.NullTime
	err := store.db.QueryRow("SELECT name, last_message_time FROM chats WHERE jid = ?", jid).Scan(&existingName, &existingTime)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if name == "" && existingName.Valid {
		name = existingName.String
	}
	if existingTime.Valid && existingTime.Time.After(lastMessageTime) {
		lastMessageTime = existingTime.Time
	}
	_, err = store.db.Exec(
		`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET name = excluded.name, last_message_time = excluded.last_message_time`,
		jid, name, lastMessageTime,
	)
	return err
}

// GetOldestMessage returns the oldest stored message of a chat that has an ID, to anchor
// on-demand history requests. It returns sql.ErrNoRows when the chat has none.
func (store *MessageStore) GetOldestMessage(chatJID string) (id string, ts time.Time, fromMe bool, err error) {
	err = store.db.QueryRow(
		`SELECT id, timestamp, is_from_me FROM messages
		WHERE chat_jid = ? AND id != ''
		ORDER BY timestamp ASC, id ASC LIMIT 1`,
		chatJID,
	).Scan(&id, &ts, &fromMe)
	return id, ts, fromMe, err
}

// EnsureChat creates a chat if it does not exist yet, leaving an existing chat's name and
// last message time untouched
func (store *MessageStore) EnsureChat(jid, name string) error {
	_, err := store.db.Exec("INSERT OR IGNORE INTO chats (jid, name) VALUES (?, ?)", jid, name)
	return err
}

// Store a message in the database
func (store *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	// Only store if there's actual content or media
	if content == "" && mediaType == "" {
		return nil
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages 
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)
	return err
}

// Get messages from a chat
func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename)
		if err != nil {
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

// Get all chats
func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil {
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

// unwrapMessage returns the message that actually carries the content.
//
// View-once, ephemeral and document-with-caption messages are envelopes: the
// real Message sits one level down. Live events are already unwrapped by
// whatsmeow, but history sync payloads are not, so without this a captioned
// image in a disappearing-messages chat is stored as if it were empty.
func unwrapMessage(msg *waProto.Message) *waProto.Message {
	// Envelopes do nest (ephemeral wrapping view-once), but the chain is short.
	// The bound is only there so a malformed payload cannot loop forever.
	for i := 0; i < 4 && msg != nil; i++ {
		switch {
		case msg.GetEphemeralMessage().GetMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage().GetMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDocumentWithCaptionMessage().GetMessage() != nil:
			msg = msg.GetDocumentWithCaptionMessage().GetMessage()
		default:
			return msg
		}
	}
	return msg
}

// Extract text content from a message
//
// A caption is text, not decoration: when someone sends a screenshot and types
// what to do with it underneath, the caption IS the message. While only
// Conversation and ExtendedTextMessage were read, such a message was stored
// with an empty content column — indistinguishable from an image sent with no
// words at all. Audio messages have no caption field in the protocol.
func extractTextContent(msg *waProto.Message) string {
	msg = unwrapMessage(msg)
	if msg == nil {
		return ""
	}

	// Try to get text content
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}

	// Media captions
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}

	return ""
}

// SendMessageResponse represents the response for the send message API
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API
type SendMessageRequest struct {
	Recipient string   `json:"recipient"`
	Message   string   `json:"message"`
	MediaPath string   `json:"media_path,omitempty"`
	Mentions  []string `json:"mentions,omitempty"`
}

// validateMediaPath decides whether a file may be sent. It closes CWE-22 (Path Traversal) by
// refusing paths that contain ".." and, when WHATSAPP_MEDIA_ROOTS is set, restricting reads to
// that list of directories. It also always refuses the bridge's own store/ (session keys and
// message history) and, unless a WHATSAPP_MEDIA_ROOTS entry covers them, hidden paths such as
// ~/.ssh or .env, so a prompt-injected send cannot exfiltrate secrets.
func validateMediaPath(mediaPath string) error {
	var roots []string
	for _, root := range strings.Split(os.Getenv("WHATSAPP_MEDIA_ROOTS"), string(os.PathListSeparator)) {
		if root != "" {
			roots = append(roots, root)
		}
	}
	storeDir, err := filepath.Abs("store")
	if err != nil {
		return fmt.Errorf("cannot locate the bridge store: %v", err)
	}
	return checkMediaPath(mediaPath, roots, storeDir)
}

// resolvePath returns the absolute path with symlinks resolved; a path that does not exist yet
// keeps its absolute form
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real), nil
	}
	return filepath.Clean(abs), nil
}

// within reports whether path is dir or inside it
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}

// hiddenComponent returns the first path component of rel starting with ".", or ""
func hiddenComponent(rel string) string {
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if strings.HasPrefix(part, ".") && part != "." {
			return part
		}
	}
	return ""
}

func checkMediaPath(mediaPath string, roots []string, storeDir string) error {
	if mediaPath == "" {
		return fmt.Errorf("media_path is empty")
	}
	if strings.Contains(mediaPath, "..") {
		return fmt.Errorf("media_path must not contain \"..\"")
	}
	abs, err := filepath.Abs(mediaPath)
	if err != nil {
		return fmt.Errorf("bad media_path: %v", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("cannot resolve media_path: %v", err)
	}
	real = filepath.Clean(real)

	if store, err := resolvePath(storeDir); err == nil && within(real, store) {
		return fmt.Errorf("media_path is inside the bridge's store/, which is never sent")
	}

	if len(roots) == 0 {
		if part := hiddenComponent(real); part != "" {
			return fmt.Errorf("media_path is under the hidden path component %q; add a folder to WHATSAPP_MEDIA_ROOTS to send from it", part)
		}
		return nil
	}
	for _, root := range roots {
		rootReal, err := resolvePath(root)
		if err != nil || !within(real, rootReal) {
			continue
		}
		// Hidden components of the root itself were chosen deliberately; any below it were not
		if part := hiddenComponent(strings.TrimPrefix(real, rootReal)); part != "" {
			return fmt.Errorf("media_path is under the hidden path component %q inside a WHATSAPP_MEDIA_ROOTS entry", part)
		}
		return nil
	}
	return fmt.Errorf("media_path is not under any WHATSAPP_MEDIA_ROOTS entry")
}

// storeSentMessage records a message the user sent through the bridge, in the same shape as a
// received one: the chat row first (for the foreign key and last message time), then the message
// as the user's own. It needs no WhatsApp client so it can be tested against a temporary database.
func storeSentMessage(store *MessageStore, chatJID types.JID, chatName, sender, msgID string, timestamp time.Time, msg *waProto.Message) error {
	if err := store.StoreChat(chatJID.String(), chatName, timestamp); err != nil {
		return fmt.Errorf("failed to store chat: %v", err)
	}

	content := extractTextContent(msg)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg, msgID, timestamp)

	if err := store.StoreMessage(msgID, chatJID.String(), sender, content, timestamp, true,
		mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength); err != nil {
		return fmt.Errorf("failed to store message: %v", err)
	}
	return nil
}

// Function to send a WhatsApp message
func sendWhatsAppMessage(client *whatsmeow.Client, messageStore *MessageStore, recipient string, message string, mediaPath string, mentions []string, logger waLog.Logger) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	// Create JID for recipient
	var recipientJID types.JID
	var err error

	// Check if recipient is a JID
	isJID := strings.Contains(recipient, "@")

	if isJID {
		// Parse the JID string
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net", // For personal chats
		}
	}

	msg := &waProto.Message{}

	// Check if we have media to send
	if mediaPath != "" {
		// CWE-22 guard - refuse traversal paths and (optionally) enforce
		// WHATSAPP_MEDIA_ROOTS before touching the filesystem.
		if err := validateMediaPath(mediaPath); err != nil {
			return false, fmt.Sprintf("Refusing media_path: %v", err)
		}
		// Read media file
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		// Determine media type and mime type based on file extension
		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		// Handle different media types
		switch fileExt {
		// Image types
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"

		// Audio types
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"

		// Video types
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"

		// Document types (for any other file type)
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		// Upload media to WhatsApp servers
		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		// Create the appropriate message type based on media type
		switch mediaType {
		case whatsmeow.MediaImage:
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaAudio:
			// Handle ogg audio files
			var seconds uint32 = 30 // Default fallback
			var waveform []byte = nil

			// Try to analyze the ogg file
			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			} else {
				fmt.Printf("Not an Ogg Opus file: %s\n", mimeType)
			}

			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
			}
		case whatsmeow.MediaVideo:
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaDocument:
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		}
	} else if len(mentions) > 0 {
		// Mentions need an extended text message whose context lists the mentioned JIDs;
		// the text itself must contain "@<phone number>" for each of them
		mentionedJIDs := make([]string, 0, len(mentions))
		for _, mention := range mentions {
			if !strings.Contains(mention, "@") {
				mention += "@" + types.DefaultUserServer
			}
			mentionedJIDs = append(mentionedJIDs, mention)
		}
		msg.ExtendedTextMessage = &waProto.ExtendedTextMessage{
			Text:        proto.String(message),
			ContextInfo: &waProto.ContextInfo{MentionedJID: mentionedJIDs},
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	// Send message
	resp, err := client.SendMessage(context.Background(), recipientJID, msg)

	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	// Record the delivered message locally, as the user's own, so reads reflect it at once.
	// WhatsApp does not echo a message back to the device that sent it.
	chatJID := resolveLID(client, recipientJID)
	chatName := GetChatName(client, messageStore, chatJID, chatJID.String(), nil, chatJID.User, logger)
	sender := ""
	if client.Store.ID != nil {
		sender = client.Store.ID.User
	}
	timestamp := resp.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	if err := storeSentMessage(messageStore, chatJID, chatName, sender, resp.ID, timestamp, msg); err != nil {
		// Already delivered: reporting a failure would only invite a duplicate send
		logger.Warnf("Failed to store sent message: %v", err)
	} else {
		content := extractTextContent(msg)
		mediaType, filename, _, _, _, _, _ := extractMediaInfo(msg, resp.ID, timestamp)
		fmt.Println(formatMessageLog(timestamp, true, chatJID.String(), sender, mediaType, filename, content, logContent))
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// legacyMediaFilename matches the filenames generated before they included the message ID,
// which used the time the bridge processed the message rather than the message's own time
var legacyMediaFilename = regexp.MustCompile(`^(image|video|audio|document)_\d{8}_\d{6}(\.(jpg|mp4|ogg))?$`)

// migrateMediaFilenames renames stored media that still has a legacy generated filename to
// the current "<type>_<message time>_<ID suffix>" scheme, so files no longer collide on download
// repairChatLastMessageTimes sets the last message time of every chat that has stored
// messages to the time of its newest one, undoing drift left by events that moved the chat
// without being stored. Chats without stored messages keep their time.
func repairChatLastMessageTimes(messageStore *MessageStore, logger waLog.Logger) error {
	result, err := messageStore.db.Exec(`
		UPDATE chats SET last_message_time = (SELECT MAX(timestamp) FROM messages WHERE chat_jid = chats.jid)
		WHERE EXISTS (SELECT 1 FROM messages WHERE chat_jid = chats.jid)
		  AND last_message_time IS NOT (SELECT MAX(timestamp) FROM messages WHERE chat_jid = chats.jid)`)
	if err != nil {
		return err
	}
	if repaired, err := result.RowsAffected(); err == nil && repaired > 0 {
		logger.Infof("Repaired the last message time of %d chats", repaired)
	}
	return nil
}

func migrateMediaFilenames(messageStore *MessageStore, logger waLog.Logger) error {
	rows, err := messageStore.db.Query("SELECT id, chat_jid, timestamp, filename FROM messages WHERE media_type != '' AND filename != ''")
	if err != nil {
		return err
	}
	type rename struct{ id, chatJID, filename string }
	var renames []rename
	for rows.Next() {
		var id, chatJID, filename string
		var timestamp time.Time
		if err := rows.Scan(&id, &chatJID, &timestamp, &filename); err != nil {
			continue
		}
		match := legacyMediaFilename.FindStringSubmatch(filename)
		if match == nil {
			continue
		}
		idSuffix := id
		if len(idSuffix) > 8 {
			idSuffix = idSuffix[len(idSuffix)-8:]
		}
		renamed := match[1] + "_" + timestamp.Local().Format("20060102_150405") + "_" + idSuffix + match[2]
		renames = append(renames, rename{id, chatJID, renamed})
	}
	rows.Close()

	for _, r := range renames {
		if _, err := messageStore.db.Exec("UPDATE messages SET filename = ? WHERE id = ? AND chat_jid = ?", r.filename, r.id, r.chatJID); err != nil {
			return err
		}
	}
	if len(renames) > 0 {
		logger.Infof("Renamed %d media files to message-time filenames", len(renames))
	}
	return nil
}

// Extract media info from a message.
// Generated filenames use the message's own timestamp plus a suffix of its ID, so that
// media replayed during history sync (dozens per second) neither gets the sync time as
// its name nor collides with, and overwrites, other files saved in the same second.
func extractMediaInfo(msg *waProto.Message, msgID string, msgTime time.Time) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	// Same envelopes as in extractTextContent: a document sent with a caption
	// arrives as DocumentWithCaptionMessage, and without unwrapping it would be
	// stored as a message with no attachment at all.
	msg = unwrapMessage(msg)
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	idSuffix := msgID
	if len(idSuffix) > 8 {
		idSuffix = idSuffix[len(idSuffix)-8:]
	}
	stamp := msgTime.Local().Format("20060102_150405") + "_" + idSuffix

	// Check for image message
	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + stamp + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}

	// Check for video message
	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + stamp + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}

	// Check for audio message
	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + stamp + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}

	// Check for document message
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + stamp
		}
		return "document", filename,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}

// Handle regular incoming messages with media support
func handleMessage(client *whatsmeow.Client, messageStore *MessageStore, msg *events.Message, logger waLog.Logger) {
	// Save message to database, using phone-number JIDs instead of LIDs so chats aren't split
	msg.Info.Chat = resolveLID(client, msg.Info.Chat)
	if msg.Info.Sender.Server == types.HiddenUserServer && msg.Info.SenderAlt.Server == types.DefaultUserServer {
		msg.Info.Sender = msg.Info.SenderAlt
	} else {
		msg.Info.Sender = resolveLID(client, msg.Info.Sender)
	}
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User

	// Get appropriate chat name (pass nil for conversation since we don't have one for regular messages)
	name := GetChatName(client, messageStore, msg.Info.Chat, chatJID, nil, sender, logger)

	// Extract text content
	content := resolveLIDMentions(client, extractTextContent(msg.Message))

	// Extract media info
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message, msg.Info.ID, msg.Info.Timestamp)

	// Events that are not stored (reactions, protocol messages, ...) must not move the chat's
	// last message time; they only make sure the chat exists
	if content == "" && mediaType == "" {
		if err := messageStore.EnsureChat(chatJID, name); err != nil {
			logger.Warnf("Failed to store chat: %v", err)
		}
		return
	}

	// Update chat in database with the message timestamp (keeps last message time updated)
	err := messageStore.StoreChat(chatJID, name, msg.Info.Timestamp)
	if err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	// Store message in database
	err = messageStore.StoreMessage(
		msg.Info.ID,
		chatJID,
		sender,
		content,
		msg.Info.Timestamp,
		msg.Info.IsFromMe,
		mediaType,
		filename,
		url,
		mediaKey,
		fileSHA256,
		fileEncSHA256,
		fileLength,
	)

	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
	} else {
		// Live, stored messages may fire message listeners (history sync never reaches here)
		notifyListeners(client, msg, name, content, mediaType, filename)

		// Log message reception (metadata only unless content logging is on)
		fmt.Println(formatMessageLog(msg.Info.Timestamp, msg.Info.IsFromMe, chatJID, sender, mediaType, filename, content, logContent))
	}
}

// DownloadMediaRequest represents the request body for the download media API
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse represents the response for the download media API
type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Store additional media info in the database
func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// Get media info from the database
func (store *MessageStore) GetMediaInfo(id, chatJID string) (string, string, string, []byte, []byte, []byte, uint64, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := store.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	return mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err
}

// MediaDownloader implements the whatsmeow.DownloadableMessage interface
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

// GetDirectPath implements the DownloadableMessage interface
func (d *MediaDownloader) GetDirectPath() string {
	return d.DirectPath
}

// GetURL implements the DownloadableMessage interface
func (d *MediaDownloader) GetURL() string {
	return d.URL
}

// GetMediaKey implements the DownloadableMessage interface
func (d *MediaDownloader) GetMediaKey() []byte {
	return d.MediaKey
}

// GetFileLength implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileLength() uint64 {
	return d.FileLength
}

// GetFileSHA256 implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileSHA256() []byte {
	return d.FileSHA256
}

// GetFileEncSHA256 implements the DownloadableMessage interface
func (d *MediaDownloader) GetFileEncSHA256() []byte {
	return d.FileEncSHA256
}

// GetMediaType implements the DownloadableMessage interface
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType {
	return d.MediaType
}

// Function to download media from a message
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	// Query the database for the message
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64
	var err error

	// First, check if we already have this file
	chatDir := fmt.Sprintf("store/%s", strings.ReplaceAll(chatJID, ":", "_"))
	localPath := ""

	// Get media info from the database
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err = messageStore.GetMediaInfo(messageID, chatJID)

	if err != nil {
		// Try to get basic info if extended info isn't available
		err = messageStore.db.QueryRow(
			"SELECT media_type, filename FROM messages WHERE id = ? AND chat_jid = ?",
			messageID, chatJID,
		).Scan(&mediaType, &filename)

		if err != nil {
			return false, "", "", "", fmt.Errorf("failed to find message: %v", err)
		}
	}

	// Check if this is a media message
	if mediaType == "" {
		return false, "", "", "", fmt.Errorf("not a media message")
	}

	// Create directory for the chat if it doesn't exist
	if err := os.MkdirAll(chatDir, 0755); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	// Generate a local path for the file
	localPath = fmt.Sprintf("%s/%s", chatDir, filename)

	// Get absolute path
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	// Check if file already exists
	if _, err := os.Stat(localPath); err == nil {
		// File exists, return it
		return true, mediaType, filename, absPath, nil
	}

	// If we don't have all the media info we need, we can't download
	if url == "" || len(mediaKey) == 0 || len(fileSHA256) == 0 || len(fileEncSHA256) == 0 || fileLength == 0 {
		return false, "", "", "", fmt.Errorf("incomplete media information for download")
	}

	fmt.Printf("Attempting to download media for message %s in chat %s...\n", messageID, chatJID)

	// Extract direct path from URL
	directPath := extractDirectPathFromURL(url)

	// Create a downloader that implements DownloadableMessage
	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return false, "", "", "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	downloader := &MediaDownloader{
		URL:           url,
		DirectPath:    directPath,
		MediaKey:      mediaKey,
		FileLength:    fileLength,
		FileSHA256:    fileSHA256,
		FileEncSHA256: fileEncSHA256,
		MediaType:     waMediaType,
	}

	// Download the media using whatsmeow client
	mediaData, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	// Save the downloaded media to file
	if err := os.WriteFile(localPath, mediaData, 0644); err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	fmt.Printf("Successfully downloaded %s media to %s (%d bytes)\n", mediaType, absPath, len(mediaData))
	return true, mediaType, filename, absPath, nil
}

// Extract direct path from a WhatsApp media URL
//
// The direct path must include the URL's query string byte-for-byte:
// whatsmeow's downloader builds the actual CDN request as
// directPath + "&hash=..." + more params, and that query carries the
// request's signature (oh=/oe=/ccb=/etc). Stripping it (the previous
// behavior here) produces an unsigned request that the CDN rejects with
// a 403 -- in practice for every media message, since observed
// production URLs of both known shapes carry a query string 100% of the
// time. Do not rebuild this with u.String()/u.Query().Encode(): both can
// re-escape or reorder the query and break the signature just as
// thoroughly as dropping it outright.
func extractDirectPathFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.EscapedPath() == "" {
		return rawURL // fall back to the original URL if parsing fails
	}

	directPath := u.EscapedPath()
	if u.RawQuery != "" {
		directPath += "?" + u.RawQuery
	}
	return directPath
}

// Start a REST API server to expose the WhatsApp client functionality
func startRESTServer(client *whatsmeow.Client, messageStore *MessageStore, port int, logger waLog.Logger) {
	// Handler for sending messages
	// Message listeners and their webhooks
	registerListenerRoutes(http.DefaultServeMux, newListenerAPI(client, messageStore, loadWebhookConfig(), port))

	// On-demand history for one chat: POST requests it, GET reads its status
	http.HandleFunc("/api/history/backfill", newClientBackfillService(client, messageStore).handleHistoryBackfill)

	http.HandleFunc("/api/send", newSendHandler(newSendGuard(sendGuardCfg, func(jid types.JID) types.JID { return resolveLID(client, jid) }, time.Now),
		func(req SendMessageRequest) (bool, string) {
			return sendWhatsAppMessage(client, messageStore, req.Recipient, req.Message, req.MediaPath, req.Mentions, logger)
		}))

	// Handler for downloading media
	http.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse the request body
		var req DownloadMediaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		// Validate request
		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
			return
		}

		// Download the media
		success, mediaType, filename, path, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID)

		// Set response headers
		w.Header().Set("Content-Type", "application/json")

		// Handle download result
		if !success || err != nil {
			errMsg := "Unknown error"
			if err != nil {
				errMsg = err.Error()
			}

			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download media: %s", errMsg),
			})
			return
		}

		// Send successful response
		json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success:  true,
			Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
			Filename: filename,
			Path:     path,
		})
	})

	// Start the server. Bind to loopback only — the REST API has no auth and
	// will send WhatsApp messages from the linked account on behalf of any caller.
	// Binding to all interfaces (":port") would expose send/read to anyone on the
	// same LAN (home WiFi, café, hotel), so we restrict to 127.0.0.1. The MCP
	// server connects via http://localhost:{port} (whatsapp-mcp-server/whatsapp.py),
	// which works unchanged. To opt into LAN exposure, set BIND_ADDR=0.0.0.0.
	bindAddr := restBindAddr()
	serverAddr := fmt.Sprintf("%s:%d", bindAddr, port)
	fmt.Printf("Starting REST API server on %s...\n", serverAddr)

	// Run server in a goroutine so it doesn't block
	go func() {
		if err := http.ListenAndServe(serverAddr, nil); err != nil {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()
}

func main() {
	// Set up logger
	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp client...")

	// Always use the bridge folder's store/ (where the MCP server reads it), whatever the
	// directory the bridge is launched from, also under `go run`
	useBridgeDir()

	// Create database connection for storing session data
	dbLog := waLog.Stdout("Database", "INFO", true)

	// Create directory for database if it doesn't exist
	if err := os.MkdirAll("store", 0755); err != nil {
		logger.Errorf("Failed to create store directory: %v", err)
		return
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", dbLog)
	if err != nil {
		logger.Errorf("Failed to connect to database: %v", err)
		return
	}

	// Get device store - This contains session information
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			// No device exists, create one
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			logger.Errorf("Failed to get device: %v", err)
			return
		}
	}

	// Create client instance
	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		logger.Errorf("Failed to create WhatsApp client")
		return
	}

	// Initialize message store
	messageStore, err := NewMessageStore()
	if err != nil {
		logger.Errorf("Failed to initialize message store: %v", err)
		return
	}
	defer messageStore.Close()

	// Message listeners and webhook delivery
	setupListeners(messageStore, loadWebhookConfig(), 8080, logger)
	logContent = loadLogContent()
	cfg, err := loadSendGuardConfig()
	if err != nil {
		logger.Errorf("%v", err)
		os.Exit(1)
	}
	sendGuardCfg = cfg
	logger.Infof("Send guardrails: %s", sendGuardCfg)

	// Setup event handling for messages and history sync
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			// Process regular messages
			handleMessage(client, messageStore, v, logger)

		case *events.HistorySync:
			// Process history sync events
			handleHistorySync(client, messageStore, v, logger)

		case *events.Connected:
			logger.Infof("Connected to WhatsApp")

		case *events.LoggedOut:
			logger.Warnf("Device logged out, please scan QR code to log in again")
		}
	})

	// Create channel to track connection success
	connected := make(chan bool, 1)

	// Connect to WhatsApp
	if client.Store.ID == nil {
		// No ID stored, this is a new client, need to pair with phone
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			logger.Errorf("Failed to connect: %v", err)
			return
		}

		// Print QR code for pairing with phone
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\nScan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "success" {
				connected <- true
				break
			}
		}

		// Wait for connection
		select {
		case <-connected:
			fmt.Println("\nSuccessfully connected and authenticated!")
		case <-time.After(3 * time.Minute):
			logger.Errorf("Timeout waiting for QR code scan")
			return
		}
	} else {
		// Already logged in, just connect
		err = client.Connect()
		if err != nil {
			logger.Errorf("Failed to connect: %v", err)
			return
		}
		connected <- true
	}

	// Wait a moment for connection to stabilize
	time.Sleep(2 * time.Second)

	if !client.IsConnected() {
		logger.Errorf("Failed to establish stable connection")
		return
	}

	// Merge chats stored under LIDs into their phone-number chats
	if err := migrateLIDChats(client, messageStore, logger); err != nil {
		logger.Warnf("Failed to migrate LID chats: %v", err)
	}

	// Rename media stored with the old sync-time filenames
	if err := migrateMediaFilenames(messageStore, logger); err != nil {
		logger.Warnf("Failed to migrate media filenames: %v", err)
	}

	// Realign chats' last message time with their newest stored message
	if err := repairChatLastMessageTimes(messageStore, logger); err != nil {
		logger.Warnf("Failed to repair chat last message times: %v", err)
	}

	fmt.Println("\n✓ Connected to WhatsApp! Type 'help' for commands.")

	// Start REST API server
	startRESTServer(client, messageStore, 8080, logger)

	// Create a channel to keep the main goroutine alive
	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("REST server is running. Press Ctrl+C to disconnect and exit.")

	// Wait for termination signal
	<-exitChan

	fmt.Println("Disconnecting...")
	webhookDeliverer.Shutdown(5 * time.Second)
	// Disconnect client
	client.Disconnect()
}

// GetChatName determines the appropriate name for a chat based on JID and other info
func GetChatName(client *whatsmeow.Client, messageStore *MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	// First, check if chat already exists in database with a name
	var existingName string
	err := messageStore.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&existingName)
	if err == nil && existingName != "" {
		// Chat exists with a name, use that
		logger.Infof("Using existing chat name for %s: %s", chatJID, existingName)
		return existingName
	}

	// Need to determine chat name
	var name string

	if jid.Server == "g.us" {
		// This is a group chat
		logger.Infof("Getting name for group: %s", chatJID)

		// Use conversation data if provided (from history sync)
		if conversation != nil {
			// Extract name from conversation if available
			// This uses type assertions to handle different possible types
			var displayName, convName *string
			// Try to extract the fields we care about regardless of the exact type
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				// Try to find DisplayName field
				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				// Try to find Name field
				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			// Use the name we found
			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		// If we didn't get a name, try group info
		if name == "" {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				// Fallback name for groups
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}

		logger.Infof("Using group name: %s", name)
	} else {
		// This is an individual contact
		logger.Infof("Getting name for contact: %s", chatJID)

		// Just use contact info (full name)
		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if sender != "" {
			// Fallback to sender
			name = sender
		} else {
			// Last fallback to JID
			name = jid.User
		}

		logger.Infof("Using contact name: %s", name)
	}

	return name
}

// Handle history sync events
// resolveLID maps a LID (hidden user) JID to its phone-number JID when the mapping is known.
// Other JIDs, and LIDs without a known mapping, are returned unchanged (minus device info).
func resolveLID(client *whatsmeow.Client, jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid)
	if err != nil || pn.IsEmpty() {
		return jid
	}
	return pn.ToNonAD()
}

// lidMentionPattern matches "@<digits>" mentions, which WhatsApp now writes using LIDs
var lidMentionPattern = regexp.MustCompile(`@(\d{6,})`)

// resolveLIDMentions rewrites "@<lid>" mentions in message text to "@<phone number>" when the mapping is known
func resolveLIDMentions(client *whatsmeow.Client, content string) string {
	if !strings.Contains(content, "@") {
		return content
	}
	return lidMentionPattern.ReplaceAllStringFunc(content, func(mention string) string {
		pn, err := client.Store.LIDs.GetPNForLID(context.Background(), types.NewJID(mention[1:], types.HiddenUserServer))
		if err != nil || pn.IsEmpty() {
			return mention
		}
		return "@" + pn.User
	})
}

// migrateLIDChats moves messages stored under LID chats and LID senders to their phone-number equivalents
func migrateLIDChats(client *whatsmeow.Client, messageStore *MessageStore, logger waLog.Logger) error {
	ctx := context.Background()

	rows, err := messageStore.db.Query("SELECT jid FROM chats WHERE jid LIKE '%@" + types.HiddenUserServer + "'")
	if err != nil {
		return err
	}
	var lidChats []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err == nil {
			lidChats = append(lidChats, jid)
		}
	}
	rows.Close()

	for _, lidChat := range lidChats {
		lid, err := types.ParseJID(lidChat)
		if err != nil {
			continue
		}
		pn := resolveLID(client, lid)
		if pn.Server != types.DefaultUserServer {
			continue
		}
		pnChat := pn.String()
		name := GetChatName(client, messageStore, pn, pnChat, nil, "", logger)

		tx, err := messageStore.db.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO chats (jid, name, last_message_time)
			SELECT ?, ?, last_message_time FROM chats WHERE jid = ?
			ON CONFLICT(jid) DO UPDATE SET last_message_time = MAX(chats.last_message_time, excluded.last_message_time)`,
			pnChat, name, lidChat)
		if err == nil {
			_, err = tx.Exec("UPDATE OR IGNORE messages SET chat_jid = ? WHERE chat_jid = ?", pnChat, lidChat)
		}
		if err == nil {
			_, err = tx.Exec("DELETE FROM messages WHERE chat_jid = ?", lidChat)
		}
		if err == nil {
			_, err = tx.Exec("DELETE FROM chats WHERE jid = ?", lidChat)
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to merge %s into %s: %v", lidChat, pnChat, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logger.Infof("Merged LID chat %s into %s", lidChat, pnChat)
	}

	// Senders are stored as the bare user part; rewrite those that are known LIDs
	rows, err = messageStore.db.Query("SELECT DISTINCT sender FROM messages WHERE is_from_me = 0")
	if err != nil {
		return err
	}
	var senders []string
	for rows.Next() {
		var sender string
		if err := rows.Scan(&sender); err == nil && sender != "" {
			senders = append(senders, sender)
		}
	}
	rows.Close()

	for _, sender := range senders {
		lid := types.NewJID(sender, types.HiddenUserServer)
		if parsed, err := types.ParseJID(sender); err == nil && parsed.Server == types.HiddenUserServer {
			lid = parsed.ToNonAD()
		}
		pn, err := client.Store.LIDs.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			continue
		}
		if _, err := messageStore.db.Exec("UPDATE messages SET sender = ? WHERE sender = ?", pn.User, sender); err != nil {
			return err
		}
	}

	// Rewrite LID mentions in already stored message text
	rows, err = messageStore.db.Query("SELECT id, chat_jid, content FROM messages WHERE content LIKE '%@%'")
	if err != nil {
		return err
	}
	type mentionUpdate struct{ id, chatJID, content string }
	var updates []mentionUpdate
	for rows.Next() {
		var u mentionUpdate
		if err := rows.Scan(&u.id, &u.chatJID, &u.content); err != nil {
			continue
		}
		if resolved := resolveLIDMentions(client, u.content); resolved != u.content {
			u.content = resolved
			updates = append(updates, u)
		}
	}
	rows.Close()

	for _, u := range updates {
		if _, err := messageStore.db.Exec("UPDATE messages SET content = ? WHERE id = ? AND chat_jid = ?", u.content, u.id, u.chatJID); err != nil {
			return err
		}
	}
	if len(updates) > 0 {
		logger.Infof("Resolved LID mentions in %d messages", len(updates))
	}

	return nil
}

// historyDeps holds the client-dependent pieces of history storage, so conversations can be
// stored (and tested) without a live WhatsApp connection
type historyDeps struct {
	resolveJID      func(types.JID) types.JID
	resolveMentions func(string) string
	chatName        func(jid types.JID, chatJID string, conversation interface{}) string
	ownUser         string
}

func clientHistoryDeps(client *whatsmeow.Client, messageStore *MessageStore, logger waLog.Logger) historyDeps {
	ownUser := ""
	if client.Store.ID != nil {
		ownUser = client.Store.ID.User
	}
	return historyDeps{
		resolveJID:      func(jid types.JID) types.JID { return resolveLID(client, jid) },
		resolveMentions: func(text string) string { return resolveLIDMentions(client, text) },
		chatName: func(jid types.JID, chatJID string, conversation interface{}) string {
			return GetChatName(client, messageStore, jid, chatJID, conversation, "", logger)
		},
		ownUser: ownUser,
	}
}

// historyConversationResult summarises one stored history conversation
type historyConversationResult struct {
	chatJID string
	stored  int
	oldest  time.Time
	newest  time.Time
}

// storeHistoryConversation stores one history sync conversation. The chat is created first
// (messages reference it), and its last message time only moves to the newest message that is
// actually stored, never backwards for an older batch.
func storeHistoryConversation(messageStore *MessageStore, conversation *waHistorySync.Conversation, deps historyDeps, logger waLog.Logger) (historyConversationResult, bool) {
	if conversation.ID == nil {
		return historyConversationResult{}, false
	}

	// Try to parse the JID
	jid, err := types.ParseJID(*conversation.ID)
	if err != nil {
		logger.Warnf("Failed to parse JID %s: %v", *conversation.ID, err)
		return historyConversationResult{}, false
	}
	jid = deps.resolveJID(jid)
	chatJID := jid.String()
	result := historyConversationResult{chatJID: chatJID}

	if len(conversation.Messages) == 0 {
		return result, true
	}

	// Get appropriate chat name by passing the history sync conversation directly
	name := deps.chatName(jid, chatJID, conversation)
	if err := messageStore.EnsureChat(chatJID, name); err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	var latest time.Time
	for _, msg := range conversation.Messages {
		if msg == nil || msg.Message == nil {
			continue
		}

		// Extract text content
		// Same path as live messages. Duplicating the extraction here
		// meant captions were dropped only for history-synced messages,
		// which is the harder half of the bug to notice.
		content := deps.resolveMentions(extractTextContent(msg.Message.Message))

		// Get message timestamp
		ts := msg.Message.GetMessageTimestamp()
		if ts == 0 {
			continue
		}
		timestamp := time.Unix(int64(ts), 0)

		// Extract media info
		var mediaType, filename, url string
		var mediaKey, fileSHA256, fileEncSHA256 []byte
		var fileLength uint64

		if msg.Message.Message != nil {
			mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message, msg.Message.GetKey().GetID(), timestamp)
		}

		// Skip messages with no content and no media
		if content == "" && mediaType == "" {
			continue
		}

		// Determine sender
		var sender string
		isFromMe := false
		if msg.Message.Key != nil {
			if msg.Message.Key.FromMe != nil {
				isFromMe = *msg.Message.Key.FromMe
			}
			// Group senders may come in the key or, in newer history syncs, in the message info itself
			participant := msg.Message.Key.GetParticipant()
			if participant == "" {
				participant = msg.Message.GetParticipant()
			}
			if !isFromMe && participant != "" {
				sender = participant
				if participantJID, err := types.ParseJID(participant); err == nil {
					sender = deps.resolveJID(participantJID).User
				}
			} else if isFromMe {
				sender = deps.ownUser
			} else {
				if jid.Server == types.GroupServer {
					logger.Warnf("No participant for group message %s in %s", msg.Message.Key.GetID(), chatJID)
				}
				sender = jid.User
			}
		} else {
			sender = jid.User
		}

		// Store message
		msgID := msg.Message.GetKey().GetID()

		err := messageStore.StoreMessage(
			msgID,
			chatJID,
			sender,
			content,
			timestamp,
			isFromMe,
			mediaType,
			filename,
			url,
			mediaKey,
			fileSHA256,
			fileEncSHA256,
			fileLength,
		)
		if err != nil {
			logger.Warnf("Failed to store history message: %v", err)
			continue
		}
		result.stored++
		if timestamp.After(latest) {
			latest = timestamp
		}
		if result.oldest.IsZero() || timestamp.Before(result.oldest) {
			result.oldest = timestamp
		}
		// Per-message lines only with content logging on; otherwise processHistorySync summarises
		if !logContent {
			continue
		}
		if mediaType != "" {
			logger.Infof("Stored message: [%s] %s -> %s: [%s: %s] %s",
				timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, mediaType, filename, content)
		} else {
			logger.Infof("Stored message: [%s] %s -> %s: %s",
				timestamp.Format("2006-01-02 15:04:05"), sender, chatJID, content)
		}
	}

	result.newest = latest

	// Only stored messages move the chat; StoreChat never moves it backwards
	if result.stored > 0 {
		if err := messageStore.StoreChat(chatJID, name, latest); err != nil {
			logger.Warnf("Failed to store chat: %v", err)
		}
	}
	return result, true
}

func handleHistorySync(client *whatsmeow.Client, messageStore *MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	fmt.Printf("Received history sync event with %d conversations\n", len(historySync.Data.Conversations))
	processHistorySync(messageStore, historySync.Data, clientHistoryDeps(client, messageStore, logger), backfills, logger)
}

// processHistorySync stores every conversation of a history sync and, for on-demand syncs,
// completes the matching backfill requests
func processHistorySync(messageStore *MessageStore, data *waHistorySync.HistorySync, deps historyDeps, tracker *backfillTracker, logger waLog.Logger) {
	onDemand := data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND
	syncedCount := 0
	for _, conversation := range data.GetConversations() {
		result, ok := storeHistoryConversation(messageStore, conversation, deps, logger)
		if !ok {
			continue
		}
		syncedCount += result.stored
		if result.stored > 0 {
			logger.Infof("History sync for %s: stored %d messages (oldest %s, newest %s)",
				result.chatJID, result.stored, result.oldest.Format("2006-01-02 15:04:05"), result.newest.Format("2006-01-02 15:04:05"))
		}

		if onDemand && tracker != nil {
			var oldest time.Time
			if _, ts, _, err := messageStore.GetOldestMessage(result.chatJID); err == nil {
				oldest = ts
			}
			more := moreAvailableFromConversation(conversation)
			tracker.complete(result.chatJID, result.stored, oldest, more)
			logger.Infof("On-demand history for %s: stored %d messages, oldest %s, more available %s",
				result.chatJID, result.stored, oldest.Format("2006-01-02 15:04:05"), describeMore(more))
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	// Try to detect if this is a valid Ogg file by checking for the "OggS" signature
	// at the beginning of the file
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	// Parse Ogg pages to find the last page with a valid granule position
	var lastGranule uint64
	var sampleRate uint32 = 48000 // Default Opus sample rate
	var preSkip uint16 = 0
	var foundOpusHead bool

	// Scan through the file looking for Ogg pages
	for i := 0; i < len(data); {
		// Check if we have enough data to read Ogg page header
		if i+27 >= len(data) {
			break
		}

		// Verify Ogg page signature
		if string(data[i:i+4]) != "OggS" {
			// Skip until next potential page
			i++
			continue
		}

		// Extract header fields
		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		// Extract segment table
		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		// Calculate page size
		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		// Check if we're looking at an OpusHead packet (should be in first few pages)
		if !foundOpusHead && pageSeqNum <= 1 {
			// Look for "OpusHead" marker in this page
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				// Found OpusHead, extract sample rate and pre-skip
				// OpusHead format: Magic(8) + Version(1) + Channels(1) + PreSkip(2) + SampleRate(4) + ...
				headPos += 8 // Skip "OpusHead" marker
				// PreSkip is 2 bytes at offset 10
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					fmt.Printf("Found OpusHead: sampleRate=%d, preSkip=%d\n", sampleRate, preSkip)
				}
			}
		}

		// Keep track of last valid granule position
		if granulePos != 0 {
			lastGranule = granulePos
		}

		// Move to next page
		i += pageSize
	}

	if !foundOpusHead {
		fmt.Println("Warning: OpusHead not found, using default values")
	}

	// Calculate duration based on granule position
	if lastGranule > 0 {
		// Formula for duration: (lastGranule - preSkip) / sampleRate
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		fmt.Printf("Calculated Opus duration from granule: %f seconds (lastGranule=%d)\n",
			durationSeconds, lastGranule)
	} else {
		// Fallback to rough estimation if granule position not found
		fmt.Println("Warning: No valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / 2000.0 // Very rough approximation
		duration = uint32(durationEstimate)
	}

	// Make sure we have a reasonable duration (at least 1 second, at most 300 seconds)
	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	// Generate waveform
	waveform = placeholderWaveform(duration)

	fmt.Printf("Ogg Opus analysis: size=%d bytes, calculated duration=%d sec, waveform=%d bytes\n",
		len(data), duration, len(waveform))

	return duration, waveform, nil
}

// min returns the smaller of x or y
func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages
// that appears natural with some variability based on the duration
func placeholderWaveform(duration uint32) []byte {
	// WhatsApp expects a 64-byte waveform for voice messages
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	// Seed the random number generator for consistent results with the same duration
	rand.Seed(int64(duration))

	// Create a more natural looking waveform with some patterns and variability
	// rather than completely random values

	// Base amplitude and frequency - longer messages get faster frequency
	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		// Position in the waveform (normalized 0-1)
		pos := float64(i) / float64(waveformLength)

		// Create a wave pattern with some randomness
		// Use multiple sine waves of different frequencies for more natural look
		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		// Add some randomness to make it look more natural
		val += (rand.Float64() - 0.5) * 15

		// Add some fade-in and fade-out effects
		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		// Center around 50 (typical voice baseline)
		val = val + 50

		// Ensure values stay within WhatsApp's expected range (0-100)
		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}
