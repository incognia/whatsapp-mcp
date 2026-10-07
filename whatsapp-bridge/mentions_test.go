package main

import (
	"testing"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var (
	ownPN  = types.NewJID("5215545163631", types.DefaultUserServer)
	ownLID = types.NewJID("154653199175812", types.HiddenUserServer)
)

func mentionText(jids ...string) *waProto.Message {
	return &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
		Text:        proto.String("hey"),
		ContextInfo: &waProto.ContextInfo{MentionedJID: jids},
	}}
}

func TestMentionsOfTheAccount(t *testing.T) {
	cases := []struct {
		name   string
		msg    *waProto.Message
		ownLID types.JID
		want   bool
	}{
		{"by LID", mentionText(ownLID.String()), ownLID, true},
		{"by PN with device", mentionText("5215545163631:17@s.whatsapp.net"), ownLID, true},
		{"someone else only", mentionText("5215515240897@s.whatsapp.net", "54314290651245@lid"), ownLID, false},
		{"own LID unknown, PN mention", mentionText(ownPN.String()), types.EmptyJID, true},
		{"own LID unknown, LID mention", mentionText(ownLID.String()), types.EmptyJID, false},
		{"ephemeral wrapped", &waProto.Message{EphemeralMessage: &waProto.FutureProofMessage{Message: mentionText(ownLID.String())}}, ownLID, true},
		{"captioned image", &waProto.Message{ImageMessage: &waProto.ImageMessage{
			Caption:     proto.String("mira"),
			ContextInfo: &waProto.ContextInfo{MentionedJID: []string{ownPN.String()}},
		}}, ownLID, true},
		{"no mentions", &waProto.Message{Conversation: proto.String("hola")}, ownLID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mentionsMe(extractMentionedJIDs(tc.msg), ownPN, tc.ownLID); got != tc.want {
				t.Errorf("mentionsMe = %v, want %v", got, tc.want)
			}
		})
	}
}
