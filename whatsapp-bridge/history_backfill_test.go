package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

const (
	pnUser  = "5215512345678"
	lidUser = "111111111111111"
)

var (
	pnJID    = types.NewJID(pnUser, types.DefaultUserServer)
	lidJID   = types.NewJID(lidUser, types.HiddenUserServer)
	groupJID = types.NewJID("120363000000000012", types.GroupServer)
)

func knownPN(jid types.JID) types.JID {
	if jid.User == lidUser {
		return pnJID
	}
	return types.EmptyJID
}

func knownLID(jid types.JID) types.JID {
	if jid.User == pnUser {
		return lidJID
	}
	return types.EmptyJID
}

func noMapping(types.JID) types.JID { return types.EmptyJID }

// fakeClock is a settable clock for the tracker
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func storeMessageAt(t *testing.T, store *MessageStore, id, chat string, ts time.Time, fromMe bool) {
	t.Helper()
	// Same order as the bridge: the chat (and its time) first, then the message
	if err := store.StoreChat(chat, "chat", ts); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreMessage(id, chat, "5215500000000", "hi", ts, fromMe, "", "", "", nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
}

// 1.2 GetOldestMessage

func TestGetOldestMessage(t *testing.T) {
	store := openTestStore(t)
	chat := pnJID.String()
	jan := time.Date(2026, 1, 10, 9, 0, 0, 0, time.Local)
	storeMessageAt(t, store, "MAR", chat, time.Date(2026, 3, 2, 9, 0, 0, 0, time.Local), false)
	storeMessageAt(t, store, "JAN", chat, jan, true)
	storeMessageAt(t, store, "MAY", chat, time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local), false)
	// An even older row without an ID can never be an anchor
	storeMessageAt(t, store, "", chat, jan.Add(-time.Hour), false)

	id, ts, fromMe, err := store.GetOldestMessage(chat)
	if err != nil {
		t.Fatal(err)
	}
	if id != "JAN" || !ts.Equal(jan) || !fromMe {
		t.Errorf("anchor = %s %v %v, want JAN %v true", id, ts, fromMe, jan)
	}

	if _, _, _, err := store.GetOldestMessage("5215599999999@s.whatsapp.net"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("empty chat err = %v, want sql.ErrNoRows", err)
	}
}

// 1.3 StoreChat never moves time backwards nor blanks a name

func TestStoreChatKeepsLaterTimeAndName(t *testing.T) {
	store := openTestStore(t)
	chat := pnJID.String()
	oct := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	if err := store.StoreChat(chat, "Alice", oct); err != nil {
		t.Fatal(err)
	}

	// An older batch leaves the time alone; an empty name does not blank the stored one
	if err := store.StoreChat(chat, "", time.Date(2025, 3, 1, 0, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if name, last := chatTime(t, store, chat); name != "Alice" || !last.Time.Equal(oct) {
		t.Errorf("after older batch = %q / %v, want Alice / %v", name, last.Time, oct)
	}

	// A newer message advances it
	newer := oct.Add(time.Hour)
	if err := store.StoreChat(chat, "Alice", newer); err != nil {
		t.Fatal(err)
	}
	if _, last := chatTime(t, store, chat); !last.Time.Equal(newer) {
		t.Errorf("after newer message = %v, want %v", last.Time, newer)
	}
}

// 2.2 JID normalisation

func TestNormaliseBackfillJID(t *testing.T) {
	cases := []struct {
		name               string
		raw                string
		pnForLID, lidForPN func(types.JID) types.JID
		storage, request   string
		wantErr            bool
	}{
		{"PN with known LID", pnJID.String(), knownPN, knownLID, pnJID.String(), lidJID.String(), false},
		{"PN without LID", pnJID.String(), noMapping, noMapping, pnJID.String(), pnJID.String(), false},
		{"PN with device part", pnUser + ":12@s.whatsapp.net", knownPN, knownLID, pnJID.String(), lidJID.String(), false},
		{"LID with known PN", lidJID.String(), knownPN, knownLID, pnJID.String(), lidJID.String(), false},
		{"LID without PN", lidJID.String(), noMapping, noMapping, lidJID.String(), lidJID.String(), false},
		{"group", groupJID.String(), knownPN, knownLID, groupJID.String(), groupJID.String(), false},
		{"status broadcast", "status@broadcast", knownPN, knownLID, "", "", true},
		{"newsletter", "120363000000000000@newsletter", knownPN, knownLID, "", "", true},
		{"garbage", "not a jid", knownPN, knownLID, "", "", true},
		{"empty", "", knownPN, knownLID, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage, request, err := normaliseBackfillJID(tc.raw, tc.pnForLID, tc.lidForPN)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v / %v", storage, request)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if storage.String() != tc.storage || request.String() != tc.request {
				t.Errorf("got %v / %v, want %s / %s", storage, request, tc.storage, tc.request)
			}
		})
	}
}

// 2.3 count validation

func TestValidateBackfillCount(t *testing.T) {
	n := func(v int) *int { return &v }
	cases := []struct {
		in      *int
		want    int
		wantErr bool
	}{
		{nil, 50, false}, {n(0), 0, true}, {n(-1), 0, true}, {n(1), 1, false},
		{n(50), 50, false}, {n(200), 200, false}, {n(201), 0, true},
	}
	for _, tc := range cases {
		got, err := validateBackfillCount(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("validateBackfillCount(%v) = %d, %v", tc.in, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "between 1 and 200") {
			t.Errorf("error %q does not name the range", err)
		}
	}
}

// 2.4 anchor and the request whatsmeow builds from it

func TestBuildBackfillAnchorRequest(t *testing.T) {
	ts := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	msg := (&whatsmeow.Client{}).BuildHistorySyncRequest(buildBackfillAnchor(lidJID, "3EB0ANCHOR", ts, true), 50)

	req := msg.GetProtocolMessage().GetPeerDataOperationRequestMessage()
	if req.GetPeerDataOperationRequestType() != waE2E.PeerDataOperationRequestType_HISTORY_SYNC_ON_DEMAND {
		t.Fatalf("request type = %v", req.GetPeerDataOperationRequestType())
	}
	od := req.GetHistorySyncOnDemandRequest()
	if od.GetChatJID() != lidJID.String() || od.GetOldestMsgID() != "3EB0ANCHOR" || !od.GetOldestMsgFromMe() ||
		od.GetOnDemandMsgCount() != 50 || od.GetOldestMsgTimestampMS() != ts.Unix() {
		t.Errorf("on-demand request = %+v", od)
	}
}

// 2.5 tracker and rate limiting

func TestBackfillTracker(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 6, 23, 0, 0, 0, time.Local)}
	tr := newBackfillTracker(clock.now)
	a, b := pnJID.String(), groupJID.String()

	if _, ok := tr.checkAllowed(a); !ok {
		t.Fatal("first request refused")
	}
	tr.start(backfillStatus{ChatJID: a, Count: 50})

	// Same chat while pending / within 30 s
	clock.advance(10 * time.Second)
	if wait, ok := tr.checkAllowed(a); ok || wait < 20*time.Second {
		t.Errorf("same chat after 10 s: wait %v ok %v, want refused with >= 20 s", wait, ok)
	}

	// Another chat within 5 s of the last accepted request
	clock.t = clock.t.Add(-8 * time.Second) // 2 s after the first request
	if wait, ok := tr.checkAllowed(b); ok || wait < 3*time.Second {
		t.Errorf("other chat after 2 s: wait %v ok %v, want refused with >= 3 s", wait, ok)
	}
	clock.advance(4 * time.Second) // 6 s after
	if _, ok := tr.checkAllowed(b); !ok {
		t.Error("other chat after 6 s refused")
	}

	// Completion
	more := true
	tr.complete(a, 12, clock.t.Add(-time.Hour), &more)
	got, _ := tr.get(a)
	if got.Status != "completed" || *got.MessagesStored != 12 || !*got.MoreAvailable {
		t.Errorf("completed status = %+v", got)
	}

	// Completing an unknown chat is a no-op
	tr.complete("5215599999999@s.whatsapp.net", 3, time.Time{}, nil)
	if _, ok := tr.get("5215599999999@s.whatsapp.net"); ok {
		t.Error("unknown chat got a status")
	}

	// Pending becomes timed_out after 120 s, and the chat may be requested again
	clock.advance(time.Minute)
	tr.start(backfillStatus{ChatJID: b, Count: 50})
	clock.advance(backfillTimeout)
	if got, _ := tr.get(b); got.Status != "timed_out" {
		t.Errorf("status after timeout = %s", got.Status)
	}
	if _, ok := tr.checkAllowed(b); !ok {
		t.Error("timed-out chat still refused")
	}
}

// 2.6 more-available flag

func TestMoreAvailableFromConversation(t *testing.T) {
	conv := func(v waHistorySync.Conversation_EndOfHistoryTransferType) *waHistorySync.Conversation {
		return &waHistorySync.Conversation{EndOfHistoryTransferType: v.Enum()}
	}
	cases := []struct {
		conv *waHistorySync.Conversation
		want string
	}{
		{&waHistorySync.Conversation{}, "unknown"},
		{conv(waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY), "true"},
		{conv(waHistorySync.Conversation_COMPLETE_BUT_MORE_MESSAGES_REMAIN_ON_PRIMARY), "true"},
		{conv(waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY), "false"},
		{conv(waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS), "false"},
	}
	for _, tc := range cases {
		if got := describeMore(moreAvailableFromConversation(tc.conv)); got != tc.want {
			t.Errorf("%v → %s, want %s", tc.conv.EndOfHistoryTransferType, got, tc.want)
		}
	}
}

// 3.1 request flow

type sendRecorder struct {
	calls int
	err   error
	last  *waE2E.Message
}

func (r *sendRecorder) send(_ context.Context, msg *waE2E.Message) error {
	r.calls++
	r.last = msg
	return r.err
}

func newTestService(t *testing.T, ready error) (*backfillService, *sendRecorder, *fakeClock) {
	t.Helper()
	store := openTestStore(t)
	storeMessageAt(t, store, "OLDEST", pnJID.String(), time.Date(2026, 1, 10, 9, 0, 0, 0, time.Local), false)
	storeMessageAt(t, store, "NEWER", pnJID.String(), time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local), true)
	clock := &fakeClock{t: time.Date(2026, 10, 6, 23, 0, 0, 0, time.Local)}
	rec := &sendRecorder{}
	return &backfillService{
		store:    store,
		tracker:  newBackfillTracker(clock.now),
		ready:    func() error { return ready },
		send:     rec.send,
		build:    (&whatsmeow.Client{}).BuildHistorySyncRequest,
		pnForLID: knownPN,
		lidForPN: knownLID,
	}, rec, clock
}

func TestRequestChatBackfillSendsOnceOnSuccess(t *testing.T) {
	svc, rec, _ := newTestService(t, nil)
	status, err := svc.requestChatBackfill(pnJID.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.calls != 1 {
		t.Fatalf("send calls = %d, want 1", rec.calls)
	}
	od := rec.last.GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
	if od.GetChatJID() != lidJID.String() || od.GetOldestMsgID() != "OLDEST" || od.GetOnDemandMsgCount() != 50 {
		t.Errorf("sent request = %+v", od)
	}
	if status.Status != "pending" || status.RequestJID != lidJID.String() || status.AnchorID != "OLDEST" || status.Count != 50 {
		t.Errorf("status = %+v", status)
	}
}

func TestRequestChatBackfillNeverSendsOnRefusal(t *testing.T) {
	count := func(v int) *int { return &v }
	cases := []struct {
		name   string
		ready  error
		jid    string
		count  *int
		status int
	}{
		{"invalid JID", nil, "status@broadcast", nil, http.StatusBadRequest},
		{"invalid count", nil, pnJID.String(), count(500), http.StatusBadRequest},
		{"disconnected", errors.New("bridge is not connected to WhatsApp"), pnJID.String(), nil, http.StatusServiceUnavailable},
		{"no anchor", nil, groupJID.String(), nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, rec, _ := newTestService(t, tc.ready)
			_, err := svc.requestChatBackfill(tc.jid, tc.count)
			var be *backfillError
			if !errors.As(err, &be) || be.status != tc.status {
				t.Fatalf("err = %v, want status %d", err, tc.status)
			}
			if rec.calls != 0 {
				t.Errorf("send called %d times", rec.calls)
			}
		})
	}

	t.Run("rate limited", func(t *testing.T) {
		svc, rec, _ := newTestService(t, nil)
		if _, err := svc.requestChatBackfill(pnJID.String(), nil); err != nil {
			t.Fatal(err)
		}
		_, err := svc.requestChatBackfill(pnJID.String(), nil)
		var be *backfillError
		if !errors.As(err, &be) || be.status != http.StatusTooManyRequests {
			t.Fatalf("err = %v, want 429", err)
		}
		if rec.calls != 1 {
			t.Errorf("send calls = %d, want 1", rec.calls)
		}
	})
}

func TestRequestChatBackfillSendFailureIsNotPending(t *testing.T) {
	svc, rec, _ := newTestService(t, nil)
	rec.err = errors.New("timeout")
	_, err := svc.requestChatBackfill(pnJID.String(), nil)
	var be *backfillError
	if !errors.As(err, &be) || be.status != http.StatusBadGateway {
		t.Fatalf("err = %v, want 502", err)
	}
	if _, ok := svc.tracker.get(pnJID.String()); ok {
		t.Error("failed send was recorded")
	}
}

// 3.2 HTTP handler

func doBackfillRequest(t *testing.T, svc *backfillService, method, target, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	rr := httptest.NewRecorder()
	svc.handleHistoryBackfill(rr, httptest.NewRequest(method, target, strings.NewReader(body)))
	var parsed map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not JSON: %q", rr.Body.String())
	}
	return rr, parsed
}

func TestHandleHistoryBackfillHTTP(t *testing.T) {
	svc, _, _ := newTestService(t, nil)
	path := "/api/history/backfill"

	rr, body := doBackfillRequest(t, svc, http.MethodGet, path+"?chat_jid="+pnJID.String(), "")
	if rr.Code != http.StatusNotFound || body["status"] != "none" {
		t.Errorf("status before any request: %d %v", rr.Code, body)
	}

	rr, body = doBackfillRequest(t, svc, http.MethodPost, path, `{"chat_jid":"`+pnJID.String()+`","count":50}`)
	if rr.Code != http.StatusAccepted || body["status"] != "pending" || body["request_jid"] != lidJID.String() ||
		body["oldest_known_id"] != "OLDEST" || body["count"] != float64(50) || body["success"] != true {
		t.Errorf("accepted: %d %v", rr.Code, body)
	}

	rr, body = doBackfillRequest(t, svc, http.MethodGet, path+"?chat_jid="+lidJID.String(), "")
	if rr.Code != http.StatusOK || body["status"] != "pending" || body["chat_jid"] != pnJID.String() {
		t.Errorf("status by LID: %d %v", rr.Code, body)
	}

	rr, _ = doBackfillRequest(t, svc, http.MethodPost, path, `{"chat_jid":"`+pnJID.String()+`"}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("repeat: %d", rr.Code)
	} else if s, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || s < 1 {
		t.Errorf("Retry-After = %q", rr.Header().Get("Retry-After"))
	}

	for _, tc := range []struct {
		method, body string
		code         int
	}{
		{http.MethodPost, `not json`, http.StatusBadRequest},
		{http.MethodPost, `{}`, http.StatusBadRequest},
		{http.MethodPost, `{"chat_jid":"` + groupJID.String() + `"}`, http.StatusNotFound},
		{http.MethodPut, `{}`, http.StatusMethodNotAllowed},
		{http.MethodDelete, ``, http.StatusMethodNotAllowed},
	} {
		svc, rec, _ := newTestService(t, nil)
		rr, body := doBackfillRequest(t, svc, tc.method, path, tc.body)
		if rr.Code != tc.code || body["success"] != false {
			t.Errorf("%s %s: %d %v, want %d", tc.method, tc.body, rr.Code, body, tc.code)
		}
		if rec.calls != 0 {
			t.Errorf("%s %s sent %d requests", tc.method, tc.body, rec.calls)
		}
	}

	down, _, _ := newTestService(t, errors.New("bridge is not connected to WhatsApp"))
	if rr, _ := doBackfillRequest(t, down, http.MethodPost, path, `{"chat_jid":"`+pnJID.String()+`"}`); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("disconnected: %d", rr.Code)
	}

	failing, rec, _ := newTestService(t, nil)
	rec.err = errors.New("boom")
	if rr, _ := doBackfillRequest(t, failing, http.MethodPost, path, `{"chat_jid":"`+pnJID.String()+`"}`); rr.Code != http.StatusBadGateway {
		t.Errorf("send failure: %d", rr.Code)
	}
}

// 4.1 / 4.2 completing requests from on-demand history syncs

func testHistoryDeps() historyDeps {
	return historyDeps{
		resolveJID: func(jid types.JID) types.JID {
			if pn := knownPN(jid); !pn.IsEmpty() {
				return pn
			}
			return jid.ToNonAD()
		},
		resolveMentions: func(s string) string { return s },
		chatName:        func(types.JID, string, interface{}) string { return "Backfilled chat" },
		ownUser:         "5215500000000",
	}
}

func onDemandSync(more waHistorySync.Conversation_EndOfHistoryTransferType, msgs ...*waHistorySync.HistorySyncMsg) *waHistorySync.HistorySync {
	return &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{
			ID:                       proto.String(lidJID.String()),
			Messages:                 msgs,
			EndOfHistoryTransferType: more.Enum(),
		}},
	}
}

func historyMsg(id, text string, ts time.Time) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(false), RemoteJID: proto.String(lidJID.String())},
		Message:          &waE2E.Message{Conversation: proto.String(text)},
		MessageTimestamp: proto.Uint64(uint64(ts.Unix())),
	}}
}

func TestOnDemandHistoryCompletesBackfill(t *testing.T) {
	svc, _, clock := newTestService(t, nil)
	if _, err := svc.requestChatBackfill(pnJID.String(), nil); err != nil {
		t.Fatal(err)
	}
	latest := time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local)
	older1 := time.Date(2025, 12, 1, 8, 0, 0, 0, time.Local)
	older2 := time.Date(2025, 11, 1, 8, 0, 0, 0, time.Local)

	clock.advance(8 * time.Second)
	data := onDemandSync(waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
		historyMsg("OLD1", "december", older1), historyMsg("OLD2", "november", older2))
	processHistorySync(svc.store, data, testHistoryDeps(), svc.tracker, waLog.Noop)

	status, _ := svc.tracker.get(pnJID.String())
	if status.Status != "completed" || *status.MessagesStored != 2 || status.MoreAvailable == nil || !*status.MoreAvailable {
		t.Errorf("status = %+v", status)
	}
	if !status.OldestTimestamp.Equal(older2) {
		t.Errorf("oldest = %v, want %v", status.OldestTimestamp, older2)
	}
	// Stored under the phone-number chat, and the chat did not move back to 2025
	if _, last := chatTime(t, svc.store, pnJID.String()); !last.Time.Equal(latest) {
		t.Errorf("last_message_time = %v, want %v", last.Time, latest)
	}

	// 4.2 the same batch again changes nothing
	var before, after int
	svc.store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&before)
	processHistorySync(svc.store, data, testHistoryDeps(), svc.tracker, waLog.Noop)
	svc.store.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&after)
	if before != after || before != 4 {
		t.Errorf("messages before/after replay = %d/%d, want 4/4", before, after)
	}
	if _, last := chatTime(t, svc.store, pnJID.String()); !last.Time.Equal(latest) {
		t.Errorf("last_message_time after replay = %v, want %v", last.Time, latest)
	}
}
