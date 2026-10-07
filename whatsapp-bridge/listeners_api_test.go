package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type apiHarness struct {
	t      *testing.T
	api    *listenerAPI
	mux    *http.ServeMux
	store  *MessageStore
	reg    *ListenerRegistry
	header http.Header
}

func newAPIHarness(t *testing.T, bindHost, token string) *apiHarness {
	t.Helper()
	store := openTestStore(t)
	reg := NewListenerRegistry(15*time.Minute, time.Now)
	d := NewDeliverer(store, waLog.Noop, 8, 1, 2*time.Second, urlPolicy{self: testSelf})
	t.Cleanup(func() { d.Shutdown(time.Second) })
	api := &listenerAPI{
		store: store, registry: reg, deliverer: d, policy: urlPolicy{self: testSelf},
		lidToPN: testLIDLookup, bindHost: bindHost, adminToken: token, now: time.Now,
	}
	mux := http.NewServeMux()
	registerListenerRoutes(mux, api)
	return &apiHarness{t: t, api: api, mux: mux, store: store, reg: reg, header: http.Header{}}
}

func (h *apiHarness) do(method, path, body string, mutate ...func(*http.Request)) (int, map[string]interface{}, http.Header) {
	h.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range h.header {
		req.Header[k] = v
	}
	for _, m := range mutate {
		m(req)
	}
	rr := httptest.NewRecorder()
	h.mux.ServeHTTP(rr, req)
	var parsed map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		h.t.Fatalf("%s %s: response is not JSON: %q", method, path, rr.Body.String())
	}
	return rr.Code, parsed, rr.Header()
}

const createBody = `{"name":"Amelia","senders":["+52 1 55 1524 0897"],"contains":["guardia"],
	"webhook_url":"http://127.0.0.1:5678/webhook/wa?auth=abc123","secret":"s3cr3t-0123456789"}`

func listenerField(body map[string]interface{}, field string) interface{} {
	return body["listener"].(map[string]interface{})[field]
}

func TestListenerCRUD(t *testing.T) {
	h := newAPIHarness(t, "127.0.0.1", "")

	code, body, _ := h.do("POST", "/api/listeners", createBody)
	if code != http.StatusCreated || body["success"] != true {
		t.Fatalf("create: %d %v", code, body)
	}
	id := int(listenerField(body, "id").(float64))
	if listenerField(body, "has_secret") != true || listenerField(body, "secret") != nil {
		t.Errorf("secret handling: %v", body)
	}
	if listenerField(body, "webhook_url") != "http://127.0.0.1:5678/webhook/wa?auth=***" {
		t.Errorf("url not masked: %v", listenerField(body, "webhook_url"))
	}
	if s := listenerField(body, "senders").([]interface{}); s[0] != "5215515240897" {
		t.Errorf("sender not normalised: %v", s)
	}
	if len(h.reg.Snapshot()) != 1 {
		t.Error("registry not reloaded after create")
	}
	path := "/api/listeners/" + strconv.Itoa(id)

	// A delivery so the list shows a last delivery summary
	h.store.db.Exec(`INSERT INTO listener_deliveries (listener_id, delivery_id, event, status, status_code) VALUES (?, 'd1', 'message', 'delivered', 200)`, id)
	code, body, _ = h.do("GET", "/api/listeners", "")
	listeners := body["listeners"].([]interface{})
	if code != 200 || len(listeners) != 1 || listeners[0].(map[string]interface{})["last_delivery"].(map[string]interface{})["status"] != "delivered" {
		t.Errorf("list: %d %v", code, body)
	}

	if code, _, _ = h.do("GET", path, ""); code != 200 {
		t.Errorf("get: %d", code)
	}

	code, body, _ = h.do("PATCH", path, `{"enabled":false}`)
	if code != 200 || listenerField(body, "enabled") != false || listenerField(body, "has_secret") != true {
		t.Errorf("disable: %d %v", code, body)
	}
	if h.reg.Snapshot()[0].Enabled {
		t.Error("registry not reloaded after PATCH")
	}
	code, body, _ = h.do("PATCH", path, `{"secret":""}`)
	if code != 200 || listenerField(body, "has_secret") != false {
		t.Errorf("secret removal: %d %v", code, body)
	}
	if code, _, _ = h.do("PATCH", path, `{"regex":"([a"}`); code != 400 {
		t.Errorf("invalid update: %d", code)
	}

	code, body, _ = h.do("GET", "/api/listeners/"+strconv.Itoa(id)+"/deliveries", "")
	if code != 200 || len(body["deliveries"].([]interface{})) != 1 {
		t.Errorf("deliveries: %d %v", code, body)
	}

	if code, _, _ = h.do("DELETE", path, ""); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _, _ = h.do("GET", path, ""); code != 404 {
		t.Errorf("get after delete: %d", code)
	}
	if len(h.reg.Snapshot()) != 0 {
		t.Error("registry not reloaded after delete")
	}
}

func TestListenerAPIErrors(t *testing.T) {
	h := newAPIHarness(t, "127.0.0.1", "")

	code, body, _ := h.do("POST", "/api/listeners", `{"match_mode":"xor","regex":"([a-z","webhook_url":"http://127.0.0.1:5678/h"}`)
	errs, _ := body["errors"].([]interface{})
	if code != 400 || body["success"] != false || len(errs) < 3 || body["error"] == "" {
		t.Errorf("invalid create: %d %v", code, body)
	}

	code, body, _ = h.do("POST", "/api/listeners/validate", createBody)
	if code != 200 || body["success"] != true {
		t.Errorf("validate: %d %v", code, body)
	}
	if n, _ := h.store.CountListeners(); n != 0 {
		t.Errorf("validate stored %d listeners", n)
	}

	if code, _, _ = h.do("GET", "/api/listeners/999", ""); code != 404 {
		t.Errorf("unknown id: %d", code)
	}
	if code, _, _ = h.do("PUT", "/api/listeners", `{}`); code != 405 {
		t.Errorf("wrong method on collection: %d", code)
	}
	if code, _, _ = h.do("PUT", "/api/listeners/1", `{}`); code != 405 {
		t.Errorf("wrong method on item: %d", code)
	}
	code, body, _ = h.do("POST", "/api/listeners", `{"name":"x","sender":["5215515240897"],"webhook_url":"http://127.0.0.1:5678/h"}`)
	if code != 400 || !strings.Contains(body["error"].(string), "unknown field") {
		t.Errorf("unknown field: %d %v", code, body)
	}
}

func TestListenerEndpointProtection(t *testing.T) {
	h := newAPIHarness(t, "127.0.0.1", "")

	code, _, _ := h.do("POST", "/api/listeners", createBody, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if code != 403 {
		t.Errorf("Origin header: %d", code)
	}
	code, _, _ = h.do("POST", "/api/listeners", createBody, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	if code != 415 {
		t.Errorf("text/plain body: %d", code)
	}
	code, _, _ = h.do("GET", "/api/listeners", "", func(r *http.Request) { r.Host = "attacker.example:8080" })
	if code != 403 {
		t.Errorf("foreign Host: %d", code)
	}
	if n, _ := h.store.CountListeners(); n != 0 {
		t.Errorf("a refused request stored %d listeners", n)
	}
	code, _, _ = h.do("GET", "/api/listeners", "", func(r *http.Request) { r.Host = "localhost:8080" })
	if code != 200 {
		t.Errorf("localhost Host: %d", code)
	}

	lan := newAPIHarness(t, "0.0.0.0", "")
	if code, _, _ = lan.do("GET", "/api/listeners", ""); code != 403 {
		t.Errorf("LAN bind without token: %d", code)
	}

	tok := newAPIHarness(t, "0.0.0.0", "a-long-admin-token")
	if code, _, _ = tok.do("GET", "/api/listeners", ""); code != 401 {
		t.Errorf("missing token: %d", code)
	}
	if code, _, _ = tok.do("GET", "/api/listeners", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _, _ = tok.do("GET", "/api/listeners", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer a-long-admin-token") }); code != 200 {
		t.Errorf("valid token: %d", code)
	}
}

func TestTestDelivery(t *testing.T) {
	recv := newReceiver(t)
	h := newAPIHarness(t, "127.0.0.1", "")
	body := strings.Replace(createBody, "http://127.0.0.1:5678/webhook/wa?auth=abc123", recv.srv.URL+"/hook", 1)
	_, created, _ := h.do("POST", "/api/listeners", body)
	id := strconv.Itoa(int(listenerField(created, "id").(float64)))
	h.do("PATCH", "/api/listeners/"+id, `{"enabled":false}`)

	code, res, _ := h.do("POST", "/api/listeners/"+id+"/test", "")
	if code != 200 || res["success"] != true || res["status"] != "delivered" {
		t.Fatalf("test delivery: %d %v", code, res)
	}
	var payload webhookPayload
	json.Unmarshal(recv.bodies[0], &payload)
	if payload.Event != "test" || recv.requests[0].Header.Get("X-Webhook-Event") != "test" ||
		recv.requests[0].Header.Get("X-Webhook-Signature") == "" {
		t.Errorf("test payload/headers: %+v", payload)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	h.do("PATCH", "/api/listeners/"+id, `{"webhook_url":"`+closedURL+`"}`)
	code, res, _ = h.do("POST", "/api/listeners/"+id+"/test", "")
	if code != 200 || res["success"] != false || res["status"] != "failed" || res["error"] == nil {
		t.Errorf("failed test delivery: %d %v", code, res)
	}

	_, deliveries, _ := h.do("GET", "/api/listeners/"+id+"/deliveries", "")
	if n := len(deliveries["deliveries"].([]interface{})); n != 2 {
		t.Errorf("test deliveries recorded = %d, want 2", n)
	}
}
