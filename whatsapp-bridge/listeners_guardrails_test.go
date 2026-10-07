package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 3.1 read-only mode refuses listener changes but keeps reads and deletes

func TestReadOnlyListenerEndpoints(t *testing.T) {
	h := newAPIHarness(t, "127.0.0.1", "")
	code, body, _ := h.do("POST", "/api/listeners", createBody)
	if code != http.StatusCreated {
		t.Fatalf("create before read-only: %d %v", code, body)
	}
	id := strconv.Itoa(int(listenerField(body, "id").(float64)))

	h.api.readOnly = true
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/listeners", createBody},
		{"PATCH", "/api/listeners/" + id, `{"enabled":false}`},
		{"POST", "/api/listeners/" + id + "/test", ""},
	} {
		code, body, _ := h.do(tc.method, tc.path, tc.body)
		if code != http.StatusForbidden || !strings.Contains(body["error"].(string), "WHATSAPP_READ_ONLY") {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
	}
	if n, _ := h.store.ListListeners(); len(n) != 1 || !n[0].Enabled {
		t.Errorf("listeners changed in read-only mode: %+v", n)
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/listeners"},
		{"GET", "/api/listeners/" + id},
		{"GET", "/api/listeners/" + id + "/deliveries"},
		{"DELETE", "/api/listeners/" + id},
	} {
		if code, body, _ := h.do(tc.method, tc.path, ""); code != http.StatusOK {
			t.Errorf("%s %s in read-only mode: %d %v", tc.method, tc.path, code, body)
		}
	}
}

// 3.2 / 3.3 listeners saved before the stricter policy

func TestExistingPublicListenerAfterUpgrade(t *testing.T) {
	h := newAPIHarness(t, "127.0.0.1", "")
	l := validListener()
	l.WebhookURL = "https://hooks.example.com/wa"
	saved, err := h.store.CreateListener(l) // stored directly, as an older bridge accepted it
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(saved.ID, 10)

	// New listeners to the same public host are refused
	code, body, _ := h.do("POST", "/api/listeners", `{"name":"x","contains":["a"],"webhook_url":"https://hooks.example.com/wa"}`)
	if code != http.StatusBadRequest || !strings.Contains(strings.ToLower(jsonString(body)), "webhook_allowed_hosts") {
		t.Errorf("create to public host: %d %v", code, body)
	}

	// Deliveries fail with an error naming the setting, without any connection
	o := h.api.deliverer.Deliver(testJob(saved.ID, l.WebhookURL, ""))
	if o.status != "failed" || !strings.Contains(o.err, "WEBHOOK_ALLOWED_HOSTS") {
		t.Errorf("delivery = %+v", o)
	}

	// The listener is kept and can still be disabled
	code, body, _ = h.do("PATCH", "/api/listeners/"+id, `{"enabled":false}`)
	if code != http.StatusOK || listenerField(body, "enabled") != false {
		t.Errorf("disable: %d %v", code, body)
	}
	// Moving it to another public host is refused
	code, _, _ = h.do("PATCH", "/api/listeners/"+id, `{"webhook_url":"https://other.example.com/wa"}`)
	if code != http.StatusBadRequest {
		t.Errorf("move to another public host: %d", code)
	}
}

func TestDialRefusesPublicAddressWithoutAllowlist(t *testing.T) {
	client := newWebhookHTTPClient(2*time.Second, urlPolicy{self: testSelf})
	start := time.Now()
	_, err := client.Post("https://93.184.216.34/hook", "application/json", nil)
	if err == nil || !strings.Contains(err.Error(), "refused by the webhook URL policy") {
		t.Fatalf("err = %v, want a policy refusal", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("refusal took %v; it should happen before any network wait", time.Since(start))
	}
}

func jsonString(v interface{}) string {
	var b strings.Builder
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			b.WriteString(k + ":" + jsonString(val) + " ")
		}
	case []interface{}:
		for _, val := range x {
			b.WriteString(jsonString(val) + " ")
		}
	case string:
		b.WriteString(x)
	}
	return b.String()
}
