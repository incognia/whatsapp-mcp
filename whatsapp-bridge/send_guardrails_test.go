package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// 1.1 settings

func TestParseFlag(t *testing.T) {
	for raw, want := range map[string]bool{"true": true, "1": true, "TRUE": true, "": false, "yes": false, "0": false} {
		if got := parseFlag(raw); got != want {
			t.Errorf("parseFlag(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseSendRate(t *testing.T) {
	cases := []struct {
		raw          string
		minute, hour int
		wantErr      bool
	}{
		{"", 0, 0, false},
		{"10/60", 10, 60, false},
		{" 2 / 5 ", 2, 5, false},
		{"0/30", 0, 30, false},
		{"10", 0, 0, true},
		{"ten/60", 0, 0, true},
		{"-1/60", 0, 0, true},
	}
	for _, tc := range cases {
		m, h, err := parseSendRate(tc.raw)
		if (err != nil) != tc.wantErr || m != tc.minute || h != tc.hour {
			t.Errorf("parseSendRate(%q) = %d, %d, %v", tc.raw, m, h, err)
		}
	}
}

func TestParseSendAllowed(t *testing.T) {
	allowed, err := parseSendAllowed("+52 1 55 0000 0001, 120363000000000000@g.us,,")
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 2 || !allowed["5215500000001@s.whatsapp.net"] || !allowed["120363000000000000@g.us"] {
		t.Errorf("allowed = %v", allowed)
	}
	if got, _ := parseSendAllowed("  "); got != nil {
		t.Errorf("empty allowlist = %v, want nil", got)
	}
	if _, err := parseSendAllowed("ana"); err == nil {
		t.Error("a name must be rejected")
	}
}

func TestSendGuardConfigStringHidesRecipients(t *testing.T) {
	cfg := sendGuardConfig{perMinute: 10, allowed: map[string]bool{"5215500000001@s.whatsapp.net": true}}
	s := cfg.String()
	if !strings.Contains(s, "1 recipients") || strings.Contains(s, "5215500000001") || !strings.Contains(s, "10/min unlimited/h") {
		t.Errorf("String() = %q", s)
	}
	if s := (sendGuardConfig{}).String(); s != "read_only=false rate_limit=off allowlist=off" {
		t.Errorf("default String() = %q", s)
	}
}

// 2.1 – 2.4 the /api/send handler

type sendCallRecorder struct{ calls []SendMessageRequest }

func (r *sendCallRecorder) send(req SendMessageRequest) (bool, string) {
	r.calls = append(r.calls, req)
	return true, "Message sent to " + req.Recipient
}

func postSend(t *testing.T, h http.HandlerFunc, body string) (int, SendMessageResponse, http.Header) {
	t.Helper()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body)))
	var resp SendMessageResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	return rr.Code, resp, rr.Header()
}

func testGuard(cfg sendGuardConfig, now func() time.Time) *sendGuard {
	g := newSendGuard(cfg, func(j types.JID) types.JID {
		if j.Server == types.HiddenUserServer {
			return types.NewJID("5215500000001", types.DefaultUserServer)
		}
		return j
	}, now)
	g.checkMed = func(string) error { return nil }
	return g
}

func TestReadOnlyRefusesSend(t *testing.T) {
	rec := &sendCallRecorder{}
	h := newSendHandler(testGuard(sendGuardConfig{readOnly: true}, time.Now), rec.send)
	code, resp, _ := postSend(t, h, `{"recipient":"5215500000001","message":"hola"}`)
	if code != http.StatusForbidden || resp.Success || !strings.Contains(resp.Message, "WHATSAPP_READ_ONLY") {
		t.Errorf("code=%d resp=%+v", code, resp)
	}
	if len(rec.calls) != 0 {
		t.Errorf("send was called %d times", len(rec.calls))
	}
}

func TestRecipientAllowlist(t *testing.T) {
	allowed, _ := parseSendAllowed("5215500000001,120363000000000000@g.us")
	rec := &sendCallRecorder{}
	h := newSendHandler(testGuard(sendGuardConfig{allowed: allowed}, time.Now), rec.send)

	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"recipient":"5215500000001","message":"hola"}`, http.StatusOK},
		{`{"recipient":"120363000000000000@g.us","message":"hola"}`, http.StatusOK},
		{`{"recipient":"99999999999999@lid","message":"hola"}`, http.StatusOK}, // LID of a listed person
		{`{"recipient":"5215500000009","message":"hola"}`, http.StatusForbidden},
	} {
		if code, resp, _ := postSend(t, h, tc.body); code != tc.want {
			t.Errorf("%s: code=%d resp=%+v", tc.body, code, resp)
		}
	}
	if len(rec.calls) != 3 {
		t.Errorf("send called %d times, want 3", len(rec.calls))
	}
}

func TestProtectedMediaRefusedWith400(t *testing.T) {
	rec := &sendCallRecorder{}
	g := testGuard(sendGuardConfig{}, time.Now)
	g.checkMed = func(p string) error { return checkMediaPath(p, nil, t.TempDir()) }
	h := newSendHandler(g, rec.send)
	home := t.TempDir()
	key := filepath.Join(home, ".ssh", "id_ed25519")
	os.MkdirAll(filepath.Dir(key), 0700)
	os.WriteFile(key, []byte("not a real key"), 0600)
	code, resp, _ := postSend(t, h, `{"recipient":"5215500000001","media_path":"`+key+`"}`)
	if code != http.StatusBadRequest || !strings.Contains(resp.Message, ".ssh") || strings.Contains(resp.Message, home) {
		t.Errorf("code=%d resp=%+v", code, resp)
	}
	if len(rec.calls) != 0 {
		t.Error("a protected file reached send")
	}
}

func TestSendRateLimit(t *testing.T) {
	body := `{"recipient":"5215500000001","message":"hola"}`

	t.Run("no limit when unset", func(t *testing.T) {
		rec := &sendCallRecorder{}
		h := newSendHandler(testGuard(sendGuardConfig{}, time.Now), rec.send)
		for i := 0; i < 30; i++ {
			if code, _, _ := postSend(t, h, body); code != http.StatusOK {
				t.Fatalf("send %d: %d", i+1, code)
			}
		}
	})

	t.Run("10/60", func(t *testing.T) {
		clock := &fakeClock{t: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
		rec := &sendCallRecorder{}
		h := newSendHandler(testGuard(sendGuardConfig{perMinute: 10, perHour: 60}, clock.now), rec.send)
		for i := 0; i < 10; i++ {
			if code, _, _ := postSend(t, h, body); code != http.StatusOK {
				t.Fatalf("send %d: %d", i+1, code)
			}
			clock.advance(time.Second)
		}
		code, resp, header := postSend(t, h, body)
		if code != http.StatusTooManyRequests || header.Get("Retry-After") != "50" || !strings.Contains(resp.Message, "WHATSAPP_SEND_RATE") {
			t.Errorf("11th: code=%d retry-after=%q resp=%+v", code, header.Get("Retry-After"), resp)
		}
		if len(rec.calls) != 10 {
			t.Errorf("send called %d times, want 10", len(rec.calls))
		}
		clock.advance(50 * time.Second)
		if code, _, _ := postSend(t, h, body); code != http.StatusOK {
			t.Errorf("after the window: %d", code)
		}
	})

	t.Run("hour window and unlimited minute", func(t *testing.T) {
		clock := &fakeClock{t: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
		h := newSendHandler(testGuard(sendGuardConfig{perMinute: 0, perHour: 5}, clock.now), (&sendCallRecorder{}).send)
		for i := 0; i < 5; i++ {
			postSend(t, h, body)
		}
		if code, _, header := postSend(t, h, body); code != http.StatusTooManyRequests || header.Get("Retry-After") != "3600" {
			t.Errorf("6th within the hour: %d, Retry-After %q", code, header.Get("Retry-After"))
		}
		clock.advance(time.Hour)
		if code, _, _ := postSend(t, h, body); code != http.StatusOK {
			t.Errorf("after an hour: %d", code)
		}
	})

	t.Run("refused sends do not count", func(t *testing.T) {
		allowed, _ := parseSendAllowed("5215500000001")
		h := newSendHandler(testGuard(sendGuardConfig{perMinute: 1, allowed: allowed}, time.Now), (&sendCallRecorder{}).send)
		postSend(t, h, `{"recipient":"5215500000009","message":"x"}`) // 403, not counted
		if code, _, _ := postSend(t, h, body); code != http.StatusOK {
			t.Errorf("first allowed send after a refusal: %d", code)
		}
	})
}

// 2.2 protected files

func TestCheckMediaPath(t *testing.T) {
	base := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(base, rel)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte("x"), 0600)
		return p
	}
	store := filepath.Join(base, "bridge", "store")
	session := mk("bridge/store/whatsapp.db")
	key := mk("home/.ssh/id_ed25519")
	env := mk("home/project/.env")
	doc := mk("home/Documents/agenda.pdf")
	outboxFile := mk("home/.local/share/outbox/report.pdf")
	hiddenInOutbox := mk("home/.local/share/outbox/.cache/x.pdf")
	link := filepath.Join(base, "home", "Documents", "session-link.db")
	os.Symlink(session, link)
	outbox := filepath.Join(base, "home", ".local", "share", "outbox")

	cases := []struct {
		name    string
		path    string
		roots   []string
		wantErr string
	}{
		{"session database", session, nil, "store/"},
		{"session database even inside a root", session, []string{base}, "store/"},
		{"symlink into store", link, nil, "store/"},
		{"ssh key", key, nil, ".ssh"},
		{"dotenv", env, nil, ".env"},
		{"ordinary document", doc, nil, ""},
		{"root under a hidden folder", outboxFile, []string{outbox}, ""},
		{"hidden folder inside a root", hiddenInOutbox, []string{outbox}, ".cache"},
		{"outside every root", doc, []string{outbox}, "not under any"},
		{"traversal", base + "/home/../home/Documents/agenda.pdf", nil, ".."},
	}
	for _, tc := range cases {
		err := checkMediaPath(tc.path, tc.roots, store)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}
