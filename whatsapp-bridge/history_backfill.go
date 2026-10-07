package main

// On-demand history backfill for one chat: the bridge asks the user's primary phone for messages
// older than the oldest one stored for that chat. The phone answers asynchronously with an
// ON_DEMAND history sync, which is stored by processHistorySync and completes the request here.
//
// Approach adapted from upstream PR lharries/whatsapp-mcp #364 and the LukasHaas/whatsapp-mcp fork.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

const (
	backfillDefaultCount   = 50
	backfillMaxCount       = 200
	backfillChatCooldown   = 30 * time.Second
	backfillGlobalCooldown = 5 * time.Second
	backfillTimeout        = 120 * time.Second
	backfillSendTimeout    = 30 * time.Second
)

// backfills tracks on-demand requests since the bridge started; shared by the REST handler and
// the history sync event handler
var backfills = newBackfillTracker(time.Now)

// backfillError carries the HTTP status and error code a failed request maps to
type backfillError struct {
	status     int
	code       string
	message    string
	retryAfter time.Duration
}

func (e *backfillError) Error() string { return e.message }

func badBackfillRequest(format string, args ...interface{}) *backfillError {
	return &backfillError{status: http.StatusBadRequest, code: "invalid_request", message: fmt.Sprintf(format, args...)}
}

// normaliseBackfillJID validates a chat JID and returns the JID data is stored under (phone
// number when known) and the JID to address the request to (the LID of a one-to-one chat when
// known, otherwise the phone-number JID; groups unchanged)
func normaliseBackfillJID(raw string, pnForLID, lidForPN func(types.JID) types.JID) (storageJID, requestJID types.JID, err error) {
	jid, parseErr := types.ParseJID(raw)
	if parseErr != nil || raw == "" || jid.User == "" {
		return types.EmptyJID, types.EmptyJID, badBackfillRequest("chat_jid %q is not a valid JID", raw)
	}
	jid = jid.ToNonAD()

	switch jid.Server {
	case types.GroupServer:
		return jid, jid, nil
	case types.DefaultUserServer:
		if lid := lidForPN(jid); !lid.IsEmpty() {
			return jid, lid.ToNonAD(), nil
		}
		return jid, jid, nil
	case types.HiddenUserServer:
		if pn := pnForLID(jid); !pn.IsEmpty() {
			return pn.ToNonAD(), jid, nil
		}
		return jid, jid, nil
	default:
		return types.EmptyJID, types.EmptyJID, badBackfillRequest("chat_jid %q is not a person or group chat", raw)
	}
}

// validateBackfillCount applies the default and the allowed range
func validateBackfillCount(count *int) (int, error) {
	if count == nil {
		return backfillDefaultCount, nil
	}
	if *count < 1 || *count > backfillMaxCount {
		return 0, badBackfillRequest("count must be between 1 and %d", backfillMaxCount)
	}
	return *count, nil
}

// buildBackfillAnchor describes the oldest stored message the way BuildHistorySyncRequest reads it
func buildBackfillAnchor(requestJID types.JID, id string, ts time.Time, fromMe bool) *types.MessageInfo {
	return &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     requestJID,
			IsFromMe: fromMe,
			IsGroup:  requestJID.Server == types.GroupServer,
		},
		ID:        id,
		Timestamp: ts,
	}
}

// moreAvailableFromConversation reports whether the phone says older history remains, or nil
// when the conversation does not say (the enum's zero value is meaningful, so presence matters)
func moreAvailableFromConversation(conv *waHistorySync.Conversation) *bool {
	if conv == nil || conv.EndOfHistoryTransferType == nil {
		return nil
	}
	var more bool
	switch conv.GetEndOfHistoryTransferType() {
	case waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
		waHistorySync.Conversation_COMPLETE_BUT_MORE_MESSAGES_REMAIN_ON_PRIMARY:
		more = true
	case waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY,
		waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS:
		more = false
	default:
		return nil
	}
	return &more
}

func describeMore(more *bool) string {
	if more == nil {
		return "unknown"
	}
	return strconv.FormatBool(*more)
}

// backfillStatus is the state of the latest request for one chat
type backfillStatus struct {
	ChatJID         string     `json:"chat_jid"`
	RequestJID      string     `json:"request_jid"`
	Count           int        `json:"count"`
	AnchorID        string     `json:"oldest_known_id"`
	AnchorTimestamp time.Time  `json:"oldest_known_timestamp"`
	RequestedAt     time.Time  `json:"requested_at"`
	Status          string     `json:"status"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	MessagesStored  *int       `json:"messages_stored,omitempty"`
	OldestTimestamp *time.Time `json:"oldest_timestamp,omitempty"`
	MoreAvailable   *bool      `json:"more_available"`
}

// backfillTracker keeps request status in memory and enforces the cooldowns
type backfillTracker struct {
	mu           sync.Mutex
	byChat       map[string]*backfillStatus
	lastAccepted time.Time
	now          func() time.Time
}

func newBackfillTracker(now func() time.Time) *backfillTracker {
	return &backfillTracker{byChat: map[string]*backfillStatus{}, now: now}
}

// expire turns a stale pending request into timed_out; the caller holds the lock
func (t *backfillTracker) expire(status *backfillStatus) {
	if status.Status == "pending" && t.now().Sub(status.RequestedAt) >= backfillTimeout {
		status.Status = "timed_out"
	}
}

// checkAllowed returns how long to wait when a new request for chat must be refused
func (t *backfillTracker) checkAllowed(chat string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()

	var wait time.Duration
	if previous, ok := t.byChat[chat]; ok {
		t.expire(previous)
		if previous.Status == "pending" {
			wait = previous.RequestedAt.Add(backfillTimeout).Sub(now)
		}
		if w := previous.RequestedAt.Add(backfillChatCooldown).Sub(now); w > wait {
			wait = w
		}
	}
	if !t.lastAccepted.IsZero() {
		if w := t.lastAccepted.Add(backfillGlobalCooldown).Sub(now); w > wait {
			wait = w
		}
	}
	return wait, wait <= 0
}

// start records an accepted request as pending
func (t *backfillTracker) start(status backfillStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status.RequestedAt = t.now()
	status.Status = "pending"
	t.byChat[status.ChatJID] = &status
	t.lastAccepted = status.RequestedAt
}

// complete marks a chat's pending request as completed; unknown chats are ignored
func (t *backfillTracker) complete(chat string, stored int, oldest time.Time, more *bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status, ok := t.byChat[chat]
	if !ok || status.Status == "completed" {
		return
	}
	now := t.now()
	status.Status = "completed"
	status.CompletedAt = &now
	status.MessagesStored = &stored
	if !oldest.IsZero() {
		status.OldestTimestamp = &oldest
	}
	status.MoreAvailable = more
}

// get returns a copy of the latest status for chat
func (t *backfillTracker) get(chat string) (backfillStatus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status, ok := t.byChat[chat]
	if !ok {
		return backfillStatus{}, false
	}
	t.expire(status)
	return *status, true
}

// backfillService wires the request flow to its dependencies so it can be tested without a
// live WhatsApp client
type backfillService struct {
	store    *MessageStore
	tracker  *backfillTracker
	ready    func() error
	send     func(ctx context.Context, msg *waE2E.Message) error
	build    func(anchor *types.MessageInfo, count int) *waE2E.Message
	pnForLID func(types.JID) types.JID
	lidForPN func(types.JID) types.JID
}

func newClientBackfillService(client *whatsmeow.Client, store *MessageStore) *backfillService {
	return &backfillService{
		store:   store,
		tracker: backfills,
		ready: func() error {
			if !client.IsConnected() || client.Store.ID == nil {
				return errors.New("bridge is not connected to WhatsApp")
			}
			return nil
		},
		send: func(ctx context.Context, msg *waE2E.Message) error {
			_, err := client.SendPeerMessage(ctx, msg)
			return err
		},
		build: client.BuildHistorySyncRequest,
		pnForLID: func(jid types.JID) types.JID {
			pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid)
			if err != nil {
				return types.EmptyJID
			}
			return pn
		},
		lidForPN: func(jid types.JID) types.JID {
			lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), jid)
			if err != nil {
				return types.EmptyJID
			}
			return lid
		},
	}
}

// requestChatBackfill validates the request, sends one on-demand history request to the phone
// and records it as pending. Nothing is sent unless every check passes.
func (s *backfillService) requestChatBackfill(rawJID string, count *int) (backfillStatus, error) {
	storageJID, requestJID, err := normaliseBackfillJID(rawJID, s.pnForLID, s.lidForPN)
	if err != nil {
		return backfillStatus{}, err
	}
	n, err := validateBackfillCount(count)
	if err != nil {
		return backfillStatus{}, err
	}
	if err := s.ready(); err != nil {
		return backfillStatus{}, &backfillError{status: http.StatusServiceUnavailable, code: "not_connected", message: err.Error()}
	}
	chat := storageJID.String()
	if wait, ok := s.tracker.checkAllowed(chat); !ok {
		return backfillStatus{}, &backfillError{
			status:     http.StatusTooManyRequests,
			code:       "rate_limited",
			message:    fmt.Sprintf("too many history requests; retry in %d seconds", retryAfterSeconds(wait)),
			retryAfter: wait,
		}
	}

	id, ts, fromMe, err := s.store.GetOldestMessage(chat)
	if errors.Is(err, sql.ErrNoRows) {
		return backfillStatus{}, &backfillError{
			status:  http.StatusNotFound,
			code:    "no_anchor",
			message: "the chat has no stored messages; at least one is needed as a starting point",
		}
	} else if err != nil {
		return backfillStatus{}, &backfillError{status: http.StatusInternalServerError, code: "store_error", message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), backfillSendTimeout)
	defer cancel()
	if err := s.send(ctx, s.build(buildBackfillAnchor(requestJID, id, ts, fromMe), n)); err != nil {
		return backfillStatus{}, &backfillError{status: http.StatusBadGateway, code: "send_failed", message: fmt.Sprintf("failed to send the history request: %v", err)}
	}

	s.tracker.start(backfillStatus{
		ChatJID:         chat,
		RequestJID:      requestJID.String(),
		Count:           n,
		AnchorID:        id,
		AnchorTimestamp: ts,
	})
	status, _ := s.tracker.get(chat)
	return status, nil
}

func retryAfterSeconds(wait time.Duration) int {
	return int(math.Ceil(wait.Seconds()))
}

// backfillResponse is the JSON shape of every /api/history/backfill answer
type backfillResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
	*backfillStatus
}

func writeBackfillJSON(w http.ResponseWriter, status int, body backfillResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeBackfillError(w http.ResponseWriter, err error) {
	var be *backfillError
	if !errors.As(err, &be) {
		be = &backfillError{status: http.StatusInternalServerError, code: "internal_error", message: err.Error()}
	}
	if be.status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(be.retryAfter)))
	}
	writeBackfillJSON(w, be.status, backfillResponse{Success: false, Code: be.code, Message: be.message})
}

// handleHistoryBackfill serves POST (request a backfill) and GET (status) on /api/history/backfill
func (s *backfillService) handleHistoryBackfill(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			ChatJID string `json:"chat_jid"`
			Count   *int   `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBackfillError(w, badBackfillRequest("invalid JSON body: %v", err))
			return
		}
		if req.ChatJID == "" {
			writeBackfillError(w, badBackfillRequest("chat_jid is required"))
			return
		}
		status, err := s.requestChatBackfill(req.ChatJID, req.Count)
		if err != nil {
			writeBackfillError(w, err)
			return
		}
		writeBackfillJSON(w, http.StatusAccepted, backfillResponse{
			Success:        true,
			Message:        "history requested; messages arrive asynchronously from the phone",
			backfillStatus: &status,
		})

	case http.MethodGet:
		raw := r.URL.Query().Get("chat_jid")
		storageJID, _, err := normaliseBackfillJID(raw, s.pnForLID, s.lidForPN)
		if err != nil {
			writeBackfillError(w, err)
			return
		}
		status, ok := s.tracker.get(storageJID.String())
		if !ok {
			writeBackfillJSON(w, http.StatusNotFound, backfillResponse{
				Success:        false,
				Code:           "none",
				Message:        "no history request for this chat since the bridge started",
				backfillStatus: &backfillStatus{ChatJID: storageJID.String(), Status: "none"},
			})
			return
		}
		writeBackfillJSON(w, http.StatusOK, backfillResponse{Success: true, backfillStatus: &status})

	default:
		w.Header().Set("Allow", "GET, POST")
		writeBackfillJSON(w, http.StatusMethodNotAllowed, backfillResponse{Success: false, Code: "method_not_allowed", Message: "use POST to request history or GET to read its status"})
	}
}
