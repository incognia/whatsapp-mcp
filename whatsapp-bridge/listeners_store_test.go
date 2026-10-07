package main

import (
	"errors"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

const testWebhook = "http://127.0.0.1:5678/webhook/wa"

func testLIDLookup(jid types.JID) (types.JID, bool) {
	if jid.User == lidUser {
		return pnJID, true
	}
	return types.EmptyJID, false
}

func TestListenerStoreRoundTrip(t *testing.T) {
	store := openTestStore(t)
	in := Listener{
		Name: "Ana", Enabled: false, MatchMode: "and",
		ChatJIDs: []string{"120363000000000011@g.us"}, Senders: []string{"5215500000011"},
		Contains: []string{"guardia", "incidente"}, Regex: `(?i)p[12]`, MentionsMe: true, IncludeFromMe: true,
		WebhookURL: testWebhook, Secret: "s3cr3t-0123456789",
	}
	created, err := store.CreateListener(in)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || !created.HasSecret || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}
	got, err := store.GetListener(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.HasSecret, in.CreatedAt, in.UpdatedAt = got.ID, true, got.CreatedAt, got.UpdatedAt
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, in)
	}

	got.Enabled = true
	got.Secret = ""
	updated, err := store.UpdateListener(got)
	if err != nil || !updated.Enabled || updated.HasSecret {
		t.Errorf("update = %+v, %v", updated, err)
	}

	all, err := store.ListListeners()
	if err != nil || len(all) != 1 {
		t.Errorf("list = %v, %v", all, err)
	}
	if n, _ := store.CountListeners(); n != 1 {
		t.Errorf("count = %d", n)
	}

	if err := store.DeleteListener(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetListener(created.ID); !errors.Is(err, errListenerNotFound) {
		t.Errorf("get after delete err = %v", err)
	}
	if err := store.DeleteListener(created.ID); !errors.Is(err, errListenerNotFound) {
		t.Errorf("second delete err = %v", err)
	}
	if _, err := store.UpdateListener(Listener{ID: 999, MatchMode: "or", WebhookURL: testWebhook}); !errors.Is(err, errListenerNotFound) {
		t.Errorf("update unknown err = %v", err)
	}
}

func TestNormaliseIdentities(t *testing.T) {
	cases := []struct {
		raw          string
		chat, sender string
		wantErr      bool
	}{
		{"+52 1 55 1234 5678", "5215512345678@s.whatsapp.net", "5215512345678", false},
		{"5215512345678@s.whatsapp.net:12", "5215512345678@s.whatsapp.net", "5215512345678", false},
		{"5215512345678:12@s.whatsapp.net", "5215512345678@s.whatsapp.net", "5215512345678", false},
		{lidUser + "@lid", "5215512345678@s.whatsapp.net", "5215512345678", false},
		{"222222222222222@lid", "222222222222222@lid", "222222222222222@lid", false},
		{"not a number", "", "", true},
		{"hello@example.com", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range cases {
		chat, err := normaliseChat(tc.raw, testLIDLookup)
		if (err != nil) != tc.wantErr || chat != tc.chat {
			t.Errorf("normaliseChat(%q) = %q, %v; want %q", tc.raw, chat, err, tc.chat)
		}
		sender, err := normaliseSender(tc.raw, testLIDLookup)
		if (err != nil) != tc.wantErr || sender != tc.sender {
			t.Errorf("normaliseSender(%q) = %q, %v; want %q", tc.raw, sender, err, tc.sender)
		}
	}

	// A group is a valid chat but not a sender
	if chat, err := normaliseChat("120363000000000011@g.us", testLIDLookup); err != nil || chat != "120363000000000011@g.us" {
		t.Errorf("group chat = %q, %v", chat, err)
	}
	if _, err := normaliseSender("120363000000000011@g.us", testLIDLookup); err == nil {
		t.Error("group accepted as a sender")
	}
}
