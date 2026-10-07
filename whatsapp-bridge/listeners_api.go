package main

// REST endpoints to manage listeners. They are guarded against requests a browser could forge
// (CSRF, DNS rebinding) and, beyond loopback, require WEBHOOK_ADMIN_TOKEN.

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxListenerBody = 64 << 10

// listenerAPI holds what the listener endpoints need
type listenerAPI struct {
	store      *MessageStore
	registry   *ListenerRegistry
	deliverer  *Deliverer
	policy     urlPolicy
	lidToPN    lidLookup
	bindHost   string
	adminToken string
	now        func() time.Time
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireLocalAdmin wraps every listener route with the protection rules of the spec
func (api *listenerAPI) requireLocalAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeAPIError(w, http.StatusForbidden, "requests from web pages are not allowed")
			return
		}
		if r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody {
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaType != "application/json" {
				writeAPIError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !isLoopbackHost(host) && !strings.EqualFold(host, api.bindHost) {
			writeAPIError(w, http.StatusForbidden, "unexpected Host header")
			return
		}
		if api.adminToken != "" {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(api.adminToken)) != 1 {
				writeAPIError(w, http.StatusUnauthorized, "a valid bearer token is required")
				return
			}
		} else if !isLoopbackHost(api.bindHost) {
			writeAPIError(w, http.StatusForbidden, "listener management needs WEBHOOK_ADMIN_TOKEN when the API is not bound to loopback")
			return
		}
		next(w, r)
	}
}

// --- responses ---

// listenerView is a listener as answered: no secret, masked URL, optional last delivery
type listenerView struct {
	Listener
	LastDelivery *deliverySummary `json:"last_delivery,omitempty"`
}

type deliverySummary struct {
	Status     string    `json:"status"`
	At         time.Time `json:"at"`
	StatusCode *int      `json:"status_code,omitempty"`
}

func viewOf(l Listener) listenerView {
	l.WebhookURL = maskURL(l.WebhookURL)
	l.Secret = ""
	return listenerView{Listener: l}
}

func writeAPIJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeAPIJSON(w, status, map[string]interface{}{"success": false, "error": message})
}

func writeValidationErrors(w http.ResponseWriter, errs []FieldError) {
	writeAPIJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": errs[0].Message, "errors": errs})
}

// decodeListenerInput reads a JSON body strictly (unknown fields are an error) and bounded
func decodeListenerInput(w http.ResponseWriter, r *http.Request) (listenerInput, bool) {
	var in listenerInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxListenerBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return in, false
	}
	return in, true
}

func (api *listenerAPI) listenerID(w http.ResponseWriter, r *http.Request) (Listener, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "listener not found")
		return Listener{}, false
	}
	l, err := api.store.GetListener(id)
	if errors.Is(err, errListenerNotFound) {
		writeAPIError(w, http.StatusNotFound, "listener not found")
		return Listener{}, false
	} else if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return Listener{}, false
	}
	return l, true
}

func (api *listenerAPI) validation(creating bool) (validationContext, error) {
	ctx := validationContext{policy: api.policy, lidToPN: api.lidToPN, creating: creating}
	if creating {
		n, err := api.store.CountListeners()
		if err != nil {
			return ctx, err
		}
		ctx.existingCount = n
	}
	return ctx, nil
}

func (api *listenerAPI) reload() {
	api.registry.Reload(api.store)
}

// --- handlers ---

func (api *listenerAPI) create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeListenerInput(w, r)
	if !ok {
		return
	}
	l := newListenerDefaults()
	in.applyTo(&l)
	ctx, err := api.validation(true)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if errs := validateListener(&l, ctx); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	created, err := api.store.CreateListener(l)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.reload()
	writeAPIJSON(w, http.StatusCreated, map[string]interface{}{"success": true, "listener": viewOf(created)})
}

func (api *listenerAPI) validate(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeListenerInput(w, r)
	if !ok {
		return
	}
	l := newListenerDefaults()
	in.applyTo(&l)
	ctx, err := api.validation(true)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if errs := validateListener(&l, ctx); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "listener": viewOf(l)})
}

func (api *listenerAPI) list(w http.ResponseWriter, r *http.Request) {
	listeners, err := api.store.ListListeners()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]listenerView, 0, len(listeners))
	for _, l := range listeners {
		v := viewOf(l)
		var s deliverySummary
		var code *int
		var finished *time.Time
		err := api.store.db.QueryRow(`SELECT status, created_at, finished_at, status_code FROM listener_deliveries
			WHERE listener_id = ? ORDER BY id DESC LIMIT 1`, l.ID).Scan(&s.Status, &s.At, &finished, &code)
		if err == nil {
			if finished != nil {
				s.At = *finished
			}
			s.StatusCode = code
			v.LastDelivery = &s
		}
		views = append(views, v)
	}
	writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "listeners": views})
}

func (api *listenerAPI) get(w http.ResponseWriter, r *http.Request) {
	if l, ok := api.listenerID(w, r); ok {
		writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "listener": viewOf(l)})
	}
}

func (api *listenerAPI) update(w http.ResponseWriter, r *http.Request) {
	l, ok := api.listenerID(w, r)
	if !ok {
		return
	}
	in, ok := decodeListenerInput(w, r)
	if !ok {
		return
	}
	in.applyTo(&l)
	ctx, _ := api.validation(false)
	if errs := validateListener(&l, ctx); len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	updated, err := api.store.UpdateListener(l)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.reload()
	writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "listener": viewOf(updated)})
}

func (api *listenerAPI) remove(w http.ResponseWriter, r *http.Request) {
	l, ok := api.listenerID(w, r)
	if !ok {
		return
	}
	if err := api.store.DeleteListener(l.ID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.reload()
	writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "listener deleted"})
}

func (api *listenerAPI) test(w http.ResponseWriter, r *http.Request) {
	l, ok := api.listenerID(w, r)
	if !ok {
		return
	}
	id := newDeliveryID()
	m := IncomingMessage{
		ID: "TEST-" + id[:8], ChatJID: "0000000000@s.whatsapp.net", ChatName: "Test chat",
		Sender: "0000000000", SenderJID: "0000000000@s.whatsapp.net", SenderName: "Test sender",
		Timestamp: api.now(), Content: "This is a test delivery from the WhatsApp bridge.",
	}
	body, err := buildPayload("test", id, l, []string{}, m)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := api.deliverer.Deliver(DeliveryJob{
		ListenerID: l.ID, ListenerName: l.Name, URL: l.WebhookURL, Secret: l.Secret, DeliveryID: id,
		Event: "test", MessageID: m.ID, ChatJID: m.ChatJID, Body: body, CreatedAt: api.now(),
	})
	result := map[string]interface{}{"success": o.status == "delivered", "status": o.status, "delivery_id": id}
	if o.statusCode != 0 {
		result["status_code"] = o.statusCode
	}
	if o.err != "" {
		result["error"] = o.err
	}
	writeAPIJSON(w, http.StatusOK, result)
}

type deliveryRecord struct {
	DeliveryID string     `json:"delivery_id"`
	Event      string     `json:"event"`
	MessageID  *string    `json:"message_id,omitempty"`
	ChatJID    *string    `json:"chat_jid,omitempty"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	StatusCode *int       `json:"status_code,omitempty"`
	Error      *string    `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (api *listenerAPI) deliveries(w http.ResponseWriter, r *http.Request) {
	l, ok := api.listenerID(w, r)
	if !ok {
		return
	}
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, 100)
	}
	rows, err := api.store.db.Query(`SELECT delivery_id, event, message_id, chat_jid, status, attempts, status_code, error,
		created_at, finished_at FROM listener_deliveries WHERE listener_id = ? ORDER BY id DESC LIMIT ?`, l.ID, limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	records := []deliveryRecord{}
	for rows.Next() {
		var d deliveryRecord
		if err := rows.Scan(&d.DeliveryID, &d.Event, &d.MessageID, &d.ChatJID, &d.Status, &d.Attempts, &d.StatusCode,
			&d.Error, &d.CreatedAt, &d.FinishedAt); err != nil {
			continue
		}
		records = append(records, d)
	}
	writeAPIJSON(w, http.StatusOK, map[string]interface{}{"success": true, "deliveries": records})
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// registerListenerRoutes adds every /api/listeners route to mux, all behind requireLocalAdmin
func registerListenerRoutes(mux *http.ServeMux, api *listenerAPI) {
	guard := api.requireLocalAdmin
	mux.HandleFunc("POST /api/listeners", guard(api.create))
	mux.HandleFunc("GET /api/listeners", guard(api.list))
	mux.HandleFunc("POST /api/listeners/validate", guard(api.validate))
	mux.HandleFunc("GET /api/listeners/{id}", guard(api.get))
	mux.HandleFunc("PATCH /api/listeners/{id}", guard(api.update))
	mux.HandleFunc("DELETE /api/listeners/{id}", guard(api.remove))
	mux.HandleFunc("POST /api/listeners/{id}/test", guard(api.test))
	mux.HandleFunc("GET /api/listeners/{id}/deliveries", guard(api.deliveries))
	// Any other method on these paths answers a JSON 405
	// (no fallback for /validate: other methods there reach the {id} routes and get 404/405)
	for _, path := range []string{"/api/listeners", "/api/listeners/{id}",
		"/api/listeners/{id}/test", "/api/listeners/{id}/deliveries"} {
		mux.HandleFunc(path, guard(methodNotAllowed))
	}
}
