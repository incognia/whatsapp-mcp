package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestParseLogContent(t *testing.T) {
	cases := map[string]bool{
		"true": true, "1": true, "TRUE": true, " True ": true,
		"yes-please": false, "": false, "0": false, "false": false, "on": false,
	}
	for raw, want := range cases {
		if got := parseLogContent(raw); got != want {
			t.Errorf("parseLogContent(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestFormatMessageLog(t *testing.T) {
	ts := time.Date(2026, 10, 6, 23, 40, 28, 0, time.Local)
	group := "120363000000000000@g.us"
	sender := "5215500000001"

	tests := []struct {
		name        string
		outgoing    bool
		mediaType   string
		filename    string
		content     string
		withContent bool
		want        string
		mustNot     []string
	}{
		{name: "text default", content: "nos vemos a las 8", want: "[2026-10-06 23:40:28] ← " + group + " " + sender + ": text (17 chars)", mustNot: []string{"nos vemos"}},
		{name: "runes counted", content: "mañana ñandú", want: "text (12 chars)"},
		{name: "media with caption default", mediaType: "image", filename: "contract-scan.jpg", content: "firmado", want: "image (7 chars caption)", mustNot: []string{"contract-scan", "firmado"}},
		{name: "media without caption default", mediaType: "document", filename: "contract.pdf", want: ": document", mustNot: []string{"contract.pdf", "chars"}},
		{name: "outgoing marker", outgoing: true, content: "hola", want: "→ " + group},
		{name: "text with content", content: "hola", withContent: true, want: sender + ": hola"},
		{name: "media with content", mediaType: "image", filename: "a.jpg", content: "cap", withContent: true, want: "[image: a.jpg] cap"},
	}
	for _, tc := range tests {
		got := formatMessageLog(ts, tc.outgoing, group, sender, tc.mediaType, tc.filename, tc.content, tc.withContent)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q does not contain %q", tc.name, got, tc.want)
		}
		for _, s := range tc.mustNot {
			if strings.Contains(got, s) {
				t.Errorf("%s: %q leaks %q", tc.name, got, s)
			}
		}
	}
}

// captureLogger records every formatted line, at any level
type captureLogger struct {
	mu    *sync.Mutex
	lines *[]string
}

func newCaptureLogger() captureLogger {
	return captureLogger{mu: &sync.Mutex{}, lines: &[]string{}}
}

func (c captureLogger) add(level, msg string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.lines = append(*c.lines, level+" "+fmt.Sprintf(msg, args...))
}
func (c captureLogger) Warnf(msg string, args ...interface{})  { c.add("WARN", msg, args...) }
func (c captureLogger) Errorf(msg string, args ...interface{}) { c.add("ERROR", msg, args...) }
func (c captureLogger) Infof(msg string, args ...interface{})  { c.add("INFO", msg, args...) }
func (c captureLogger) Debugf(msg string, args ...interface{}) { c.add("DEBUG", msg, args...) }
func (c captureLogger) Sub(string) waLog.Logger                { return c }

func (c captureLogger) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(*c.lines, "\n")
}

func TestHistorySyncLogsSummaryWithoutContent(t *testing.T) {
	prev := logContent
	t.Cleanup(func() { logContent = prev })

	for _, withContent := range []bool{false, true} {
		logContent = withContent
		store := openTestStore(t)
		logger := newCaptureLogger()
		older := time.Date(2025, 11, 1, 8, 0, 0, 0, time.Local)
		newer := time.Date(2025, 12, 1, 8, 0, 0, 0, time.Local)
		data := onDemandSync(waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
			historyMsg("L1", "secreto uno", older), historyMsg("L2", "secreto dos", newer))
		processHistorySync(store, data, testHistoryDeps(), nil, logger)

		out := logger.all()
		if n := strings.Count(out, "stored 2 messages"); n != 1 {
			t.Errorf("content=%v: %d summary lines, want 1:\n%s", withContent, n, out)
		}
		if !strings.Contains(out, "2025-11-01 08:00:00") || !strings.Contains(out, "2025-12-01 08:00:00") {
			t.Errorf("content=%v: summary lacks the time range:\n%s", withContent, out)
		}
		if strings.Contains(out, "Message content:") {
			t.Errorf("content=%v: per-message content line still logged:\n%s", withContent, out)
		}
		if leaked := strings.Contains(out, "secreto"); leaked != withContent {
			t.Errorf("content=%v: text in logs = %v:\n%s", withContent, leaked, out)
		}
	}
}

func TestSendResultForLog(t *testing.T) {
	failed := "Error reading media file: open /Users/me/Documents/contract.pdf: no such file or directory"
	if got := sendResultForLog(failed, false); got != "Error reading media file" {
		t.Errorf("default = %q", got)
	}
	if got := sendResultForLog(failed, true); got != failed {
		t.Errorf("with content = %q", got)
	}
	if got := sendResultForLog("Message sent to 5215500000001", false); got != "Message sent to 5215500000001" {
		t.Errorf("success = %q", got)
	}
}
