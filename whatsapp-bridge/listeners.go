package main

// Message listeners: persisted rules that watch live messages and, when one matches, queue a
// webhook delivery. Model and match-mode naming follow AdamRussak/whatsapp-mcp (ADR 0001),
// with criteria grouped per field so a listener can never be impossible to match.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

const (
	maxListeners         = 50
	maxListenerName      = 100
	maxRegexLength       = 500
	maxContainsEntries   = 20
	maxContainsLength    = 200
	maxIdentityEntries   = 50
	minSecretLength      = 16
	maxPayloadContentLen = 4096
	dedupeCapacity       = 4096
)

var errListenerNotFound = errors.New("listener not found")

// Listener is one stored rule. Secret is write-only: it is never serialised in answers.
type Listener struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	MatchMode     string    `json:"match_mode"`
	ChatJIDs      []string  `json:"chat_jids"`
	Senders       []string  `json:"senders"`
	Contains      []string  `json:"contains"`
	Regex         string    `json:"regex"`
	MentionsMe    bool      `json:"mentions_me"`
	IncludeFromMe bool      `json:"include_from_me"`
	WebhookURL    string    `json:"webhook_url"`
	Secret        string    `json:"-"`
	HasSecret     bool      `json:"has_secret"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// listenerInput is a create or update body; nil fields are left as they are (or default)
type listenerInput struct {
	Name          *string   `json:"name"`
	Enabled       *bool     `json:"enabled"`
	MatchMode     *string   `json:"match_mode"`
	ChatJIDs      *[]string `json:"chat_jids"`
	Senders       *[]string `json:"senders"`
	Contains      *[]string `json:"contains"`
	Regex         *string   `json:"regex"`
	MentionsMe    *bool     `json:"mentions_me"`
	IncludeFromMe *bool     `json:"include_from_me"`
	WebhookURL    *string   `json:"webhook_url"`
	Secret        *string   `json:"secret"`
}

// newListenerDefaults is the listener a create body is applied to
func newListenerDefaults() Listener {
	return Listener{Enabled: true, MatchMode: "or", ChatJIDs: []string{}, Senders: []string{}, Contains: []string{}}
}

// applyTo copies the set fields onto l
func (in listenerInput) applyTo(l *Listener) {
	if in.Name != nil {
		l.Name = *in.Name
	}
	if in.Enabled != nil {
		l.Enabled = *in.Enabled
	}
	if in.MatchMode != nil {
		l.MatchMode = *in.MatchMode
	}
	if in.ChatJIDs != nil {
		l.ChatJIDs = *in.ChatJIDs
	}
	if in.Senders != nil {
		l.Senders = *in.Senders
	}
	if in.Contains != nil {
		l.Contains = *in.Contains
	}
	if in.Regex != nil {
		l.Regex = *in.Regex
	}
	if in.MentionsMe != nil {
		l.MentionsMe = *in.MentionsMe
	}
	if in.IncludeFromMe != nil {
		l.IncludeFromMe = *in.IncludeFromMe
	}
	if in.WebhookURL != nil {
		l.WebhookURL = *in.WebhookURL
	}
	// "secret": "" removes it; an omitted secret keeps it
	if in.Secret != nil {
		l.Secret = *in.Secret
	}
	l.HasSecret = l.Secret != ""
}

// --- storage ---

const listenerColumns = `id, name, enabled, match_mode, chat_jids, senders, contains, regex,
	mentions_me, include_from_me, webhook_url, secret, created_at, updated_at`

func scanListener(row interface{ Scan(...any) error }) (Listener, error) {
	var l Listener
	var chats, senders, contains string
	err := row.Scan(&l.ID, &l.Name, &l.Enabled, &l.MatchMode, &chats, &senders, &contains, &l.Regex,
		&l.MentionsMe, &l.IncludeFromMe, &l.WebhookURL, &l.Secret, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return l, errListenerNotFound
	} else if err != nil {
		return l, err
	}
	for _, f := range []struct {
		raw string
		dst *[]string
	}{{chats, &l.ChatJIDs}, {senders, &l.Senders}, {contains, &l.Contains}} {
		if err := json.Unmarshal([]byte(f.raw), f.dst); err != nil {
			return l, fmt.Errorf("listener %d has a corrupt list: %v", l.ID, err)
		}
		if *f.dst == nil {
			*f.dst = []string{}
		}
	}
	l.HasSecret = l.Secret != ""
	return l, nil
}

func jsonList(values []string) string {
	if values == nil {
		values = []string{}
	}
	b, _ := json.Marshal(values)
	return string(b)
}

// CreateListener stores a validated listener and returns it with its new ID
func (store *MessageStore) CreateListener(l Listener) (Listener, error) {
	res, err := store.db.Exec(`INSERT INTO listeners
		(name, enabled, match_mode, chat_jids, senders, contains, regex, mentions_me, include_from_me, webhook_url, secret)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Name, l.Enabled, l.MatchMode, jsonList(l.ChatJIDs), jsonList(l.Senders), jsonList(l.Contains), l.Regex,
		l.MentionsMe, l.IncludeFromMe, l.WebhookURL, l.Secret)
	if err != nil {
		return Listener{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Listener{}, err
	}
	return store.GetListener(id)
}

// GetListener returns one listener, or errListenerNotFound
func (store *MessageStore) GetListener(id int64) (Listener, error) {
	return scanListener(store.db.QueryRow("SELECT "+listenerColumns+" FROM listeners WHERE id = ?", id))
}

// ListListeners returns every listener ordered by ID
func (store *MessageStore) ListListeners() ([]Listener, error) {
	rows, err := store.db.Query("SELECT " + listenerColumns + " FROM listeners ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	listeners := []Listener{}
	for rows.Next() {
		l, err := scanListener(rows)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, l)
	}
	return listeners, rows.Err()
}

// CountListeners returns how many listeners exist
func (store *MessageStore) CountListeners() (int, error) {
	var n int
	err := store.db.QueryRow("SELECT COUNT(*) FROM listeners").Scan(&n)
	return n, err
}

// UpdateListener replaces a listener's stored fields
func (store *MessageStore) UpdateListener(l Listener) (Listener, error) {
	res, err := store.db.Exec(`UPDATE listeners SET name = ?, enabled = ?, match_mode = ?, chat_jids = ?, senders = ?,
		contains = ?, regex = ?, mentions_me = ?, include_from_me = ?, webhook_url = ?, secret = ?,
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		l.Name, l.Enabled, l.MatchMode, jsonList(l.ChatJIDs), jsonList(l.Senders), jsonList(l.Contains), l.Regex,
		l.MentionsMe, l.IncludeFromMe, l.WebhookURL, l.Secret, l.ID)
	if err != nil {
		return Listener{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Listener{}, errListenerNotFound
	}
	return store.GetListener(l.ID)
}

// DeleteListener removes a listener and, through the foreign key, its delivery log
func (store *MessageStore) DeleteListener(id int64) error {
	res, err := store.db.Exec("DELETE FROM listeners WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errListenerNotFound
	}
	return nil
}

// --- identifier normalisation ---

// lidLookup maps a LID JID to its phone-number JID when the mapping is known
type lidLookup func(types.JID) (types.JID, bool)

var phoneNumberPattern = regexp.MustCompile(`^\d{6,20}$`)

// bareNumber strips "+", spaces, dashes, dots and brackets; ok when what is left is a number
func bareNumber(raw string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '+', ' ', '-', '.', '(', ')':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	return cleaned, phoneNumberPattern.MatchString(cleaned)
}

// parseIdentity turns a phone number or JID into a device-free JID with a LID mapped to its
// phone number when known
func parseIdentity(raw string, lidToPN lidLookup) (types.JID, error) {
	if digits, ok := bareNumber(raw); ok {
		return types.NewJID(digits, types.DefaultUserServer), nil
	}
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "@") {
		return types.EmptyJID, fmt.Errorf("%q is neither a phone number nor a WhatsApp JID", raw)
	}
	// Accept "<number>@s.whatsapp.net:12" (device written after the server) as well as the usual form
	if at := strings.Index(raw, "@"); at >= 0 {
		if colon := strings.Index(raw[at:], ":"); colon >= 0 {
			raw = raw[:at+colon]
		}
	}
	jid, err := types.ParseJID(raw)
	if err != nil || jid.User == "" {
		return types.EmptyJID, fmt.Errorf("%q is not a valid WhatsApp JID", raw)
	}
	jid = jid.ToNonAD()
	switch jid.Server {
	case types.DefaultUserServer:
		if !phoneNumberPattern.MatchString(jid.User) {
			return types.EmptyJID, fmt.Errorf("%q is not a valid phone-number JID", raw)
		}
	case types.HiddenUserServer:
		if pn, ok := lidToPN(jid); ok {
			return pn.ToNonAD(), nil
		}
	case types.GroupServer:
	default:
		return types.EmptyJID, fmt.Errorf("%q is not a person or group JID", raw)
	}
	return jid, nil
}

// normaliseChat returns the chat JID a listener stores: <digits>@s.whatsapp.net for numbers,
// groups unchanged, LIDs mapped when known
func normaliseChat(raw string, lidToPN lidLookup) (string, error) {
	jid, err := parseIdentity(raw, lidToPN)
	if err != nil {
		return "", err
	}
	return jid.String(), nil
}

// normaliseSender returns the sender a listener stores: the phone number's digits, or an
// unmapped LID JID as given
func normaliseSender(raw string, lidToPN lidLookup) (string, error) {
	jid, err := parseIdentity(raw, lidToPN)
	if err != nil {
		return "", err
	}
	switch jid.Server {
	case types.DefaultUserServer:
		return jid.User, nil
	case types.GroupServer:
		return "", fmt.Errorf("%q is a group, not a sender", raw)
	}
	return jid.String(), nil
}

// --- validation ---

// FieldError names one problem with a listener
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// validationContext is everything validation needs besides the listener
type validationContext struct {
	policy        urlPolicy
	lidToPN       lidLookup
	creating      bool
	existingCount int
	// keepURL is the saved webhook_url of the listener being updated; it is not re-checked
	keepURL string
}

// validateListener normalises l in place and returns every problem found (empty when valid)
func validateListener(l *Listener, ctx validationContext) []FieldError {
	var errs []FieldError
	add := func(field, format string, args ...interface{}) {
		errs = append(errs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	l.Name = strings.TrimSpace(l.Name)
	if l.Name == "" {
		add("name", "name is required")
	} else if utf8.RuneCountInString(l.Name) > maxListenerName {
		add("name", "name must be at most %d characters", maxListenerName)
	}

	if l.MatchMode != "or" && l.MatchMode != "and" {
		add("match_mode", `match_mode must be "or" or "and", not %q`, l.MatchMode)
	}

	if l.Regex != "" {
		if utf8.RuneCountInString(l.Regex) > maxRegexLength {
			add("regex", "regex must be at most %d characters", maxRegexLength)
		} else if _, err := regexp.Compile(l.Regex); err != nil {
			add("regex", "regex does not compile: %v", err)
		}
	}

	if len(l.Contains) > maxContainsEntries {
		add("contains", "contains may hold at most %d entries", maxContainsEntries)
	}
	for _, c := range l.Contains {
		if strings.TrimSpace(c) == "" {
			add("contains", "contains entries must not be empty")
			break
		}
		if utf8.RuneCountInString(c) > maxContainsLength {
			add("contains", "contains entries must be at most %d characters", maxContainsLength)
			break
		}
	}

	normaliseList := func(field string, values []string, normalise func(string, lidLookup) (string, error)) []string {
		if len(values) > maxIdentityEntries {
			add(field, "%s may hold at most %d entries", field, maxIdentityEntries)
			return values
		}
		out := make([]string, 0, len(values))
		seen := map[string]bool{}
		for _, v := range values {
			n, err := normalise(v, ctx.lidToPN)
			if err != nil {
				add(field, "%v", err)
				continue
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		return out
	}
	l.ChatJIDs = normaliseList("chat_jids", l.ChatJIDs, normaliseChat)
	l.Senders = normaliseList("senders", l.Senders, normaliseSender)

	if len(l.ChatJIDs) == 0 && len(l.Senders) == 0 && len(l.Contains) == 0 && l.Regex == "" && !l.MentionsMe {
		add("criteria", "at least one criterion is required (chat_jids, senders, contains, regex or mentions_me)")
	}

	if ctx.keepURL == "" || l.WebhookURL != ctx.keepURL {
		if err := validateWebhookURL(l.WebhookURL, ctx.policy); err != nil {
			add("webhook_url", "%v", err)
		}
	}

	if l.Secret != "" && utf8.RuneCountInString(l.Secret) < minSecretLength {
		add("secret", "secret must be at least %d characters", minSecretLength)
	}
	l.HasSecret = l.Secret != ""

	if ctx.creating && ctx.existingCount >= maxListeners {
		add("listeners", "at most %d listeners can exist", maxListeners)
	}
	return errs
}

// --- matching ---

// IncomingMessage is a stored live message as listeners see it
type IncomingMessage struct {
	ID         string
	ChatJID    string
	ChatName   string
	Sender     string // phone number (user part), or an unmapped LID JID
	SenderJID  string
	SenderName string
	Timestamp  time.Time
	Content    string
	MediaType  string
	Filename   string
	IsFromMe   bool
	IsEdit     bool
	MentionsMe bool
}

// compiledListener is a listener prepared for fast matching
type compiledListener struct {
	Listener
	chats    map[string]bool
	senders  map[string]bool
	contains []string
	regex    *regexp.Regexp
}

func compileListener(l Listener) (*compiledListener, error) {
	cl := &compiledListener{Listener: l, chats: map[string]bool{}, senders: map[string]bool{}}
	for _, c := range l.ChatJIDs {
		cl.chats[c] = true
	}
	for _, s := range l.Senders {
		cl.senders[s] = true
	}
	for _, c := range l.Contains {
		cl.contains = append(cl.contains, strings.ToLower(c))
	}
	if l.Regex != "" {
		re, err := regexp.Compile(l.Regex)
		if err != nil {
			return nil, err
		}
		cl.regex = re
	}
	return cl, nil
}

// match decides whether the listener fires and which set criteria matched, in spec order
func (cl *compiledListener) match(m IncomingMessage) ([]string, bool) {
	var set, matched []string
	check := func(name string, isSet, ok bool) {
		if !isSet {
			return
		}
		set = append(set, name)
		if ok {
			matched = append(matched, name)
		}
	}

	check("chat_jids", len(cl.chats) > 0, cl.chats[m.ChatJID])
	senderUser, _, _ := strings.Cut(m.SenderJID, "@")
	check("senders", len(cl.senders) > 0, cl.senders[m.Sender] || cl.senders[m.SenderJID] ||
		(strings.HasSuffix(m.SenderJID, "@"+types.DefaultUserServer) && cl.senders[senderUser]))

	lower := strings.ToLower(m.Content)
	containsHit := false
	if m.Content != "" {
		for _, needle := range cl.contains {
			if strings.Contains(lower, needle) {
				containsHit = true
				break
			}
		}
	}
	check("contains", len(cl.contains) > 0, containsHit)
	check("regex", cl.regex != nil, m.Content != "" && cl.regex != nil && cl.regex.MatchString(m.Content))
	check("mentions_me", cl.MentionsMe, m.MentionsMe)

	if len(set) == 0 {
		return nil, false
	}
	if cl.MatchMode == "and" {
		return matched, len(matched) == len(set)
	}
	return matched, len(matched) > 0
}

// --- mentions ---

// extractMentionedJIDs collects ContextInfo.MentionedJID from whichever content carries it
func extractMentionedJIDs(msg *waProto.Message) []string {
	msg = unwrapMessage(msg)
	if msg == nil {
		return nil
	}
	var ctxs []*waProto.ContextInfo
	if m := msg.GetExtendedTextMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	if m := msg.GetImageMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	if m := msg.GetVideoMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	if m := msg.GetDocumentMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	if m := msg.GetAudioMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	if m := msg.GetStickerMessage(); m != nil {
		ctxs = append(ctxs, m.GetContextInfo())
	}
	var jids []string
	for _, c := range ctxs {
		jids = append(jids, c.GetMentionedJID()...)
	}
	return jids
}

// mentionsMe reports whether any mentioned JID is the account, by phone number or LID
func mentionsMe(jids []string, ownPN, ownLID types.JID) bool {
	for _, raw := range jids {
		jid, err := types.ParseJID(raw)
		if err != nil {
			continue
		}
		jid = jid.ToNonAD()
		switch {
		case jid.Server == types.DefaultUserServer && !ownPN.IsEmpty() && jid.User == ownPN.User:
			return true
		case jid.Server == types.HiddenUserServer && !ownLID.IsEmpty() && jid.User == ownLID.User:
			return true
		}
	}
	return false
}

// --- registry and evaluation ---

// DeliveryJob is one queued webhook request
type DeliveryJob struct {
	ListenerID   int64
	ListenerName string
	URL          string
	Secret       string
	DeliveryID   string
	Event        string
	MessageID    string
	ChatJID      string
	Body         []byte
	Attempt      int
	CreatedAt    time.Time
}

// webhookPayload is the versioned delivery body
type webhookPayload struct {
	Version    int             `json:"version"`
	Event      string          `json:"event"`
	DeliveryID string          `json:"delivery_id"`
	Listener   payloadListener `json:"listener"`
	MatchMode  string          `json:"match_mode"`
	Matched    []string        `json:"matched"`
	Message    payloadMessage  `json:"message"`
}

type payloadListener struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type payloadMessage struct {
	ID               string `json:"id"`
	ChatJID          string `json:"chat_jid"`
	ChatName         string `json:"chat_name"`
	IsGroup          bool   `json:"is_group"`
	Sender           string `json:"sender"`
	SenderJID        string `json:"sender_jid"`
	SenderName       string `json:"sender_name"`
	Timestamp        string `json:"timestamp"`
	Content          string `json:"content"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
	MediaType        string `json:"media_type"`
	Filename         string `json:"filename"`
	IsFromMe         bool   `json:"is_from_me"`
	MentionsMe       bool   `json:"mentions_me"`
}

func newDeliveryID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// buildPayload renders the delivery body for a match
func buildPayload(event, deliveryID string, l Listener, matched []string, m IncomingMessage) ([]byte, error) {
	content := m.Content
	truncated := false
	if utf8.RuneCountInString(content) > maxPayloadContentLen {
		content = string([]rune(content)[:maxPayloadContentLen])
		truncated = true
	}
	if matched == nil {
		matched = []string{}
	}
	nameOr := func(name, fallback string) string {
		if name != "" {
			return name
		}
		return fallback
	}
	return json.Marshal(webhookPayload{
		Version:    1,
		Event:      event,
		DeliveryID: deliveryID,
		Listener:   payloadListener{ID: l.ID, Name: l.Name},
		MatchMode:  l.MatchMode,
		Matched:    matched,
		Message: payloadMessage{
			ID:               m.ID,
			ChatJID:          m.ChatJID,
			ChatName:         nameOr(m.ChatName, strings.Split(m.ChatJID, "@")[0]),
			IsGroup:          strings.HasSuffix(m.ChatJID, "@"+types.GroupServer),
			Sender:           m.Sender,
			SenderJID:        m.SenderJID,
			SenderName:       nameOr(m.SenderName, m.Sender),
			Timestamp:        m.Timestamp.UTC().Format(time.RFC3339),
			Content:          content,
			ContentTruncated: truncated,
			MediaType:        m.MediaType,
			Filename:         m.Filename,
			IsFromMe:         m.IsFromMe,
			MentionsMe:       m.MentionsMe,
		},
	})
}

// dedupeSet remembers recent (listener, chat, message) triples so each fires once
type dedupeSet struct {
	mu    sync.Mutex
	seen  map[string]bool
	order []string
	next  int
}

func newDedupeSet(capacity int) *dedupeSet {
	return &dedupeSet{seen: map[string]bool{}, order: make([]string, capacity)}
}

// add returns false when key was already present
func (d *dedupeSet) add(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen[key] {
		return false
	}
	if old := d.order[d.next]; old != "" {
		delete(d.seen, old)
	}
	d.order[d.next] = key
	d.next = (d.next + 1) % len(d.order)
	d.seen[key] = true
	return true
}

// ListenerRegistry holds the compiled listeners as an immutable snapshot swapped atomically, so
// evaluation on the WhatsApp event path takes no lock and runs no SQL
type ListenerRegistry struct {
	snapshot atomic.Pointer[[]*compiledListener]
	dedupe   *dedupeSet
	maxAge   time.Duration
	now      func() time.Time
}

func NewListenerRegistry(maxAge time.Duration, now func() time.Time) *ListenerRegistry {
	r := &ListenerRegistry{dedupe: newDedupeSet(dedupeCapacity), maxAge: maxAge, now: now}
	empty := []*compiledListener{}
	r.snapshot.Store(&empty)
	return r
}

// Reload rebuilds the snapshot from the database
func (r *ListenerRegistry) Reload(store *MessageStore) error {
	listeners, err := store.ListListeners()
	if err != nil {
		return err
	}
	compiled := make([]*compiledListener, 0, len(listeners))
	for _, l := range listeners {
		cl, err := compileListener(l)
		if err != nil {
			return fmt.Errorf("listener %d: %v", l.ID, err)
		}
		compiled = append(compiled, cl)
	}
	r.snapshot.Store(&compiled)
	return nil
}

// Snapshot returns the current compiled listeners
func (r *ListenerRegistry) Snapshot() []*compiledListener {
	return *r.snapshot.Load()
}

// Evaluate returns one delivery job per listener that fires for m. It only reads memory.
func (r *ListenerRegistry) Evaluate(m IncomingMessage) []DeliveryJob {
	listeners := r.Snapshot()
	if len(listeners) == 0 || m.IsEdit || m.ChatJID == types.StatusBroadcastJID.String() {
		return nil
	}
	if r.maxAge > 0 && r.now().Sub(m.Timestamp) > r.maxAge {
		return nil
	}

	var jobs []DeliveryJob
	for _, cl := range listeners {
		if !cl.Enabled || (m.IsFromMe && !cl.IncludeFromMe) {
			continue
		}
		matched, ok := cl.match(m)
		if !ok {
			continue
		}
		if !r.dedupe.add(fmt.Sprintf("%d|%s|%s", cl.ID, m.ChatJID, m.ID)) {
			continue
		}
		id := newDeliveryID()
		body, err := buildPayload("message", id, cl.Listener, matched, m)
		if err != nil {
			continue
		}
		jobs = append(jobs, DeliveryJob{
			ListenerID: cl.ID, ListenerName: cl.Name, URL: cl.WebhookURL, Secret: cl.Secret,
			DeliveryID: id, Event: "message", MessageID: m.ID, ChatJID: m.ChatJID, Body: body, CreatedAt: r.now(),
		})
	}
	return jobs
}
