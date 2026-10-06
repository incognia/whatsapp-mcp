package main

import (
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
)

// Media replayed during history sync must be named after the message, not the sync,
// and two messages sharing the same second must not get the same filename.
func TestMediaFilenameUsesMessageTimeAndID(t *testing.T) {
	msgTime := time.Date(2026, 10, 2, 21, 25, 28, 0, time.Local)
	img := &waProto.Message{ImageMessage: &waProto.ImageMessage{}}

	_, first, _, _, _, _, _ := extractMediaInfo(img, "AC83E971FA419C6074E8D6D4389E8CF3", msgTime)
	_, second, _, _, _, _, _ := extractMediaInfo(img, "AC6AEEAA6824BF3C3C276890DBFCA083", msgTime)

	if want := "image_20261002_212528_389E8CF3.jpg"; first != want {
		t.Errorf("filename = %q, want %q", first, want)
	}
	if first == second {
		t.Errorf("two media messages in the same second got the same filename %q", first)
	}
}

func TestDocumentKeepsItsOwnFilename(t *testing.T) {
	doc := &waProto.Message{DocumentMessage: &waProto.DocumentMessage{FileName: ptr("report.pdf")}}

	_, filename, _, _, _, _, _ := extractMediaInfo(doc, "3EB0ABCDEF123456", time.Unix(0, 0))
	if filename != "report.pdf" {
		t.Errorf("filename = %q, want %q", filename, "report.pdf")
	}
}
