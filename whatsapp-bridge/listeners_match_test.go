package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	opsGroup  = "120363000000000011@g.us"
	otherChat = "120363000000000012@g.us"
	ana       = "5215500000011"
)

func compiled(t *testing.T, mutate func(*Listener)) *compiledListener {
	t.Helper()
	l := newListenerDefaults()
	l.ID, l.Name, l.WebhookURL = 7, "test", testWebhook
	mutate(&l)
	if errs := validateListener(&l, validCtx()); len(errs) != 0 {
		t.Fatalf("invalid test listener: %+v", errs)
	}
	cl, err := compileListener(l)
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func msgIn(chat, sender, text string) IncomingMessage {
	return IncomingMessage{
		ID: "3EB0" + strings.ToUpper(strings.ReplaceAll(text, " ", ""))[:min(6, len(text))], ChatJID: chat,
		Sender: sender, SenderJID: sender + "@s.whatsapp.net", Content: text, Timestamp: time.Now(),
	}
}

func TestCriterionSemantics(t *testing.T) {
	contains := compiled(t, func(l *Listener) { l.Contains = []string{"guardia"} })
	img := msgIn(otherChat, ana, "Mañana me toca GUARDIA")
	img.MediaType = "image"
	if _, ok := contains.match(img); !ok {
		t.Error("contains should be case-insensitive and read captions")
	}

	voice := msgIn(otherChat, ana, "")
	voice.MediaType = "audio"
	if _, ok := contains.match(voice); ok {
		t.Error("voice note without text matched contains")
	}

	re := compiled(t, func(l *Listener) { l.Regex = `(?i)\bincidente\s+P[12]\b` })
	if _, ok := re.match(msgIn(otherChat, ana, "Incidente p1 en producción")); !ok {
		t.Error("regex with (?i) should match")
	}
	strict := compiled(t, func(l *Listener) { l.Regex = `Incidente` })
	if _, ok := strict.match(msgIn(otherChat, ana, "incidente")); ok {
		t.Error("regex is case-sensitive without flags")
	}

	byJID := compiled(t, func(l *Listener) { l.Senders = []string{ana} })
	m := msgIn(otherChat, "", "hola")
	m.SenderJID = ana + "@s.whatsapp.net"
	if _, ok := byJID.match(m); !ok {
		t.Error("senders should also match the sender JID")
	}
}

func TestMatchModes(t *testing.T) {
	or := compiled(t, func(l *Listener) { l.ChatJIDs = []string{opsGroup}; l.Contains = []string{"guardia"} })
	and := compiled(t, func(l *Listener) {
		l.MatchMode = "and"
		l.ChatJIDs = []string{opsGroup}
		l.Contains = []string{"guardia"}
	})

	elsewhere := msgIn(otherChat, ana, "guardia")
	if matched, ok := or.match(elsewhere); !ok || strings.Join(matched, ",") != "contains" {
		t.Errorf("OR elsewhere = %v %v", matched, ok)
	}
	if _, ok := and.match(elsewhere); ok {
		t.Error("AND fired outside the chat")
	}
	inGroup := msgIn(opsGroup, ana, "¿Quién está de guardia?")
	if matched, ok := and.match(inGroup); !ok || strings.Join(matched, ",") != "chat_jids,contains" {
		t.Errorf("AND in group = %v %v", matched, ok)
	}

	lists := compiled(t, func(l *Listener) {
		l.MatchMode = "and"
		l.ChatJIDs = []string{opsGroup, otherChat}
		l.Senders = []string{ana}
	})
	if _, ok := lists.match(msgIn(otherChat, ana, "hola")); !ok {
		t.Error("list values must stay alternatives under AND")
	}
}

func TestMentionsMeCriterion(t *testing.T) {
	inGroup := compiled(t, func(l *Listener) {
		l.MatchMode = "and"
		l.ChatJIDs = []string{opsGroup}
		l.MentionsMe = true
	})
	m := msgIn(opsGroup, ana, "@yo revisa esto")
	m.MentionsMe = true
	if matched, ok := inGroup.match(m); !ok || strings.Join(matched, ",") != "chat_jids,mentions_me" {
		t.Errorf("mention in group = %v %v", matched, ok)
	}
	m.ChatJID = otherChat
	if _, ok := inGroup.match(m); ok {
		t.Error("mention in another group fired an AND listener on Ops")
	}
	m.ChatJID, m.MentionsMe = opsGroup, false
	if _, ok := inGroup.match(m); ok {
		t.Error("message without a mention fired")
	}
}

func newTestRegistry(t *testing.T, now time.Time, listeners ...func(*Listener)) (*ListenerRegistry, *MessageStore) {
	t.Helper()
	store := openTestStore(t)
	for i, mutate := range listeners {
		l := newListenerDefaults()
		l.Name, l.WebhookURL = "L"+string(rune('A'+i)), testWebhook
		mutate(&l)
		if errs := validateListener(&l, validCtx()); len(errs) != 0 {
			t.Fatalf("invalid: %+v", errs)
		}
		if _, err := store.CreateListener(l); err != nil {
			t.Fatal(err)
		}
	}
	r := NewListenerRegistry(15*time.Minute, func() time.Time { return now })
	if err := r.Reload(store); err != nil {
		t.Fatal(err)
	}
	return r, store
}

func TestRegistryReloadFollowsWrites(t *testing.T) {
	r, store := newTestRegistry(t, time.Now(), func(l *Listener) { l.Contains = []string{"a"} })
	if len(r.Snapshot()) != 1 {
		t.Fatalf("snapshot = %d", len(r.Snapshot()))
	}
	l := r.Snapshot()[0].Listener
	l.Enabled = false
	if _, err := store.UpdateListener(l); err != nil {
		t.Fatal(err)
	}
	r.Reload(store)
	if r.Snapshot()[0].Enabled {
		t.Error("update not reflected")
	}
	store.DeleteListener(l.ID)
	r.Reload(store)
	if len(r.Snapshot()) != 0 {
		t.Error("delete not reflected")
	}
}

func TestEvaluateFilters(t *testing.T) {
	now := time.Date(2026, 10, 6, 23, 0, 0, 0, time.UTC)
	r, _ := newTestRegistry(t, now,
		func(l *Listener) { l.Contains = []string{"guardia"} },
		func(l *Listener) { l.Contains = []string{"guardia"}; l.IncludeFromMe = true },
		func(l *Listener) { l.Contains = []string{"guardia"}; l.Enabled = false },
	)
	base := func() IncomingMessage {
		m := msgIn(opsGroup, ana, "guardia")
		m.Timestamp = now.Add(-time.Minute)
		return m
	}

	if jobs := r.Evaluate(base()); len(jobs) != 2 {
		t.Errorf("two enabled listeners should fire once each, got %d", len(jobs))
	}
	if jobs := r.Evaluate(base()); len(jobs) != 0 {
		t.Errorf("duplicate message fired %d times", len(jobs))
	}

	own := base()
	own.ID, own.IsFromMe = "OWN1", true
	if jobs := r.Evaluate(own); len(jobs) != 1 || jobs[0].ListenerName != "LB" {
		t.Errorf("own message: %d jobs", len(jobs))
	}

	stale := base()
	stale.ID, stale.Timestamp = "OLD1", now.Add(-90*time.Minute)
	if jobs := r.Evaluate(stale); len(jobs) != 0 {
		t.Error("stale message fired")
	}
	edit := base()
	edit.ID, edit.IsEdit = "EDIT1", true
	if jobs := r.Evaluate(edit); len(jobs) != 0 {
		t.Error("edit fired")
	}
	status := base()
	status.ID, status.ChatJID = "ST1", "status@broadcast"
	if jobs := r.Evaluate(status); len(jobs) != 0 {
		t.Error("status broadcast fired")
	}
}

func TestPayloadForGroupMessage(t *testing.T) {
	l := Listener{ID: 7, Name: "Ops on-call", MatchMode: "and"}
	m := IncomingMessage{
		ID: "3EB0ABC", ChatJID: opsGroup, ChatName: "Ops", Sender: ana,
		SenderJID: ana + "@s.whatsapp.net", SenderName: "Ana",
		Timestamp: time.Date(2026, 10, 7, 0, 4, 5, 0, time.FixedZone("CST", -6*3600)),
		Content:   "¿Quién está de guardia?",
	}
	body, err := buildPayload("message", "d-1", l, []string{"chat_jids", "contains"}, m)
	if err != nil {
		t.Fatal(err)
	}
	golden := `{"version":1,"event":"message","delivery_id":"d-1","listener":{"id":7,"name":"Ops on-call"},` +
		`"match_mode":"and","matched":["chat_jids","contains"],"message":{"id":"3EB0ABC","chat_jid":"120363000000000011@g.us",` +
		`"chat_name":"Ops","is_group":true,"sender":"5215500000011","sender_jid":"5215500000011@s.whatsapp.net",` +
		`"sender_name":"Ana","timestamp":"2026-10-07T06:04:05Z","content":"¿Quién está de guardia?","media_type":"",` +
		`"filename":"","is_from_me":false,"mentions_me":false}}`
	if string(body) != golden {
		t.Errorf("payload:\n got %s\nwant %s", body, golden)
	}

	m.Content = strings.Repeat("á", maxPayloadContentLen+10)
	body, _ = buildPayload("message", "d-2", l, nil, m)
	var p webhookPayload
	json.Unmarshal(body, &p)
	if !p.Message.ContentTruncated || len([]rune(p.Message.Content)) != maxPayloadContentLen || p.Matched == nil {
		t.Errorf("truncation: truncated=%v len=%d matched=%v", p.Message.ContentTruncated, len([]rune(p.Message.Content)), p.Matched)
	}
}
