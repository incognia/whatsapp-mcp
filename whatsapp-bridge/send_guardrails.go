package main

// Send guardrails: limits on what can leave the account that a prompt-injected model cannot
// negotiate, because every send goes through the bridge. Read-only mode, the rate limit and the
// recipient allowlist are opt-in; protected files (validateMediaPath) are always enforced.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/types"
)

// sendGuardConfig holds the guardrail settings read at start-up
type sendGuardConfig struct {
	readOnly  bool
	perMinute int // 0 = unlimited
	perHour   int // 0 = unlimited
	allowed   map[string]bool
}

// parseFlag accepts only "true" or "1" (any case)
func parseFlag(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	return v == "true" || v == "1"
}

// parseSendRate reads "<per minute>/<per hour>"; empty means no limit
func parseSendRate(raw string) (perMinute, perHour int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, nil
	}
	minute, hour, ok := strings.Cut(raw, "/")
	if !ok {
		return 0, 0, fmt.Errorf("WHATSAPP_SEND_RATE must be <per minute>/<per hour>, for example 10/60")
	}
	perMinute, err1 := strconv.Atoi(strings.TrimSpace(minute))
	perHour, err2 := strconv.Atoi(strings.TrimSpace(hour))
	if err1 != nil || err2 != nil || perMinute < 0 || perHour < 0 {
		return 0, 0, fmt.Errorf("WHATSAPP_SEND_RATE must be two non-negative numbers, for example 10/60")
	}
	return perMinute, perHour, nil
}

// normaliseRecipient turns a phone number or JID into the JID string sends and the allowlist are
// compared by, resolving LIDs to phone numbers as stored messages are
func normaliseRecipient(raw string, resolve func(types.JID) types.JID) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "@") {
		jid, err := types.ParseJID(raw)
		if err != nil {
			return "", err
		}
		if resolve != nil {
			jid = resolve(jid)
		}
		return strings.ToLower(jid.ToNonAD().String()), nil
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)
	if digits == "" {
		return "", fmt.Errorf("%q is not a phone number or JID", raw)
	}
	return digits + "@" + types.DefaultUserServer, nil
}

// parseSendAllowed reads the comma-separated recipient allowlist; nil means anyone
func parseSendAllowed(raw string) (map[string]bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	allowed := map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		jid, err := normaliseRecipient(entry, nil)
		if err != nil {
			return nil, fmt.Errorf("WHATSAPP_SEND_ALLOWED: %v", err)
		}
		allowed[jid] = true
	}
	return allowed, nil
}

// loadSendGuardConfig reads the settings from the environment
func loadSendGuardConfig() (sendGuardConfig, error) {
	cfg := sendGuardConfig{readOnly: parseFlag(os.Getenv("WHATSAPP_READ_ONLY"))}
	var err error
	if cfg.perMinute, cfg.perHour, err = parseSendRate(os.Getenv("WHATSAPP_SEND_RATE")); err != nil {
		return cfg, err
	}
	if cfg.allowed, err = parseSendAllowed(os.Getenv("WHATSAPP_SEND_ALLOWED")); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// String describes the settings for the start-up line, never listing allowed recipients
func (c sendGuardConfig) String() string {
	rate := "off"
	if c.perMinute > 0 || c.perHour > 0 {
		rate = fmt.Sprintf("%s/min %s/h", limitText(c.perMinute), limitText(c.perHour))
	}
	allow := "off"
	if c.allowed != nil {
		allow = fmt.Sprintf("%d recipients", len(c.allowed))
	}
	return fmt.Sprintf("read_only=%v rate_limit=%s allowlist=%s", c.readOnly, rate, allow)
}

func limitText(n int) string {
	if n == 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

// sendLimiter is a sliding-window limiter over the last minute and hour
type sendLimiter struct {
	mu        sync.Mutex
	perMinute int
	perHour   int
	now       func() time.Time
	sent      []time.Time
}

func newSendLimiter(perMinute, perHour int, now func() time.Time) *sendLimiter {
	return &sendLimiter{perMinute: perMinute, perHour: perHour, now: now}
}

// allow records a send if both windows have room; otherwise it reports how long to wait
func (l *sendLimiter) allow() (bool, time.Duration) {
	if l.perMinute == 0 && l.perHour == 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	keep := l.sent[:0]
	for _, t := range l.sent {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	l.sent = keep

	var wait time.Duration
	if l.perHour > 0 && len(l.sent) >= l.perHour {
		wait = l.sent[len(l.sent)-l.perHour].Add(time.Hour).Sub(now)
	}
	if l.perMinute > 0 {
		var lastMinute []time.Time
		for _, t := range l.sent {
			if now.Sub(t) < time.Minute {
				lastMinute = append(lastMinute, t)
			}
		}
		if len(lastMinute) >= l.perMinute {
			if w := lastMinute[len(lastMinute)-l.perMinute].Add(time.Minute).Sub(now); w > wait {
				wait = w
			}
		}
	}
	if wait > 0 {
		return false, wait
	}
	l.sent = append(l.sent, now)
	return true, 0
}

// sendGuard applies the guardrails to one send request, in order: read-only, allowlist,
// protected files, rate limit. It returns the HTTP status and reason of a refusal, or 0.
type sendGuard struct {
	cfg      sendGuardConfig
	limiter  *sendLimiter
	resolve  func(types.JID) types.JID
	checkMed func(string) error
}

func newSendGuard(cfg sendGuardConfig, resolve func(types.JID) types.JID, now func() time.Time) *sendGuard {
	return &sendGuard{cfg: cfg, limiter: newSendLimiter(cfg.perMinute, cfg.perHour, now), resolve: resolve, checkMed: validateMediaPath}
}

func (g *sendGuard) check(req SendMessageRequest) (status int, reason string, retryAfter int) {
	if g.cfg.readOnly {
		return 403, "sending is disabled (WHATSAPP_READ_ONLY)", 0
	}
	if g.cfg.allowed != nil {
		jid, err := normaliseRecipient(req.Recipient, g.resolve)
		if err != nil {
			return 400, fmt.Sprintf("invalid recipient: %v", err), 0
		}
		if !g.cfg.allowed[jid] {
			return 403, "recipient is not in WHATSAPP_SEND_ALLOWED", 0
		}
	}
	if req.MediaPath != "" {
		if err := g.checkMed(req.MediaPath); err != nil {
			return 400, fmt.Sprintf("Refusing media_path: %v", err), 0
		}
	}
	if ok, wait := g.limiter.allow(); !ok {
		return 429, "send rate limit reached (WHATSAPP_SEND_RATE)", max(1, retryAfterSeconds(wait))
	}
	return 0, "", 0
}

// sendGuardCfg is loaded once in main and read by the HTTP handlers
var sendGuardCfg sendGuardConfig

// newSendHandler serves /api/send: request validation, then the guardrails, then the send
func newSendHandler(guard *sendGuard, send func(SendMessageRequest) (bool, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}
		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}
		if req.Message == "" && req.MediaPath == "" {
			http.Error(w, "Message or media path is required", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if status, reason, retryAfter := guard.check(req); status != 0 {
			fmt.Printf("Send refused to %s: %s\n", req.Recipient, sendResultForLog(reason, logContent))
			if retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(SendMessageResponse{Success: false, Message: reason})
			return
		}

		// Never log the media path; the text only with content logging on
		if logContent {
			fmt.Printf("Send request to %s (media=%v, mentions=%d): %s\n", req.Recipient, req.MediaPath != "", len(req.Mentions), req.Message)
		} else {
			fmt.Printf("Send request to %s (media=%v, mentions=%d, %d chars)\n", req.Recipient, req.MediaPath != "", len(req.Mentions), utf8.RuneCountInString(req.Message))
		}
		success, message := send(req)
		fmt.Printf("Send result: success=%v, %s\n", success, sendResultForLog(message, logContent))
		if !success {
			w.WriteHeader(http.StatusInternalServerError)
		}
		json.NewEncoder(w).Encode(SendMessageResponse{Success: success, Message: message})
	}
}
