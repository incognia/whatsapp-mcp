package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

const testSecret = "s3cr3t-0123456789"

// receiver records what it gets and answers with the given status codes in order
type receiver struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	codes    []int
	delay    time.Duration
	srv      *httptest.Server
}

func newReceiver(t *testing.T, codes ...int) *receiver {
	t.Helper()
	r := &receiver{codes: codes}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, req)
		r.bodies = append(r.bodies, body)
		code := http.StatusOK
		if n := len(r.requests); n <= len(r.codes) {
			code = r.codes[n-1]
		}
		delay := r.delay
		r.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				return
			}
		}
		if code == http.StatusFound {
			w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// newTestDeliverer uses a tiny backoff so retries finish in milliseconds
func newTestDeliverer(t *testing.T, queue, workers int, timeout time.Duration) (*Deliverer, *MessageStore, int64) {
	t.Helper()
	store := openTestStore(t)
	l := validListener()
	created, err := store.CreateListener(l)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDeliverer(store, waLog.Noop, queue, workers, timeout, urlPolicy{self: testSelf})
	d.backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { d.Shutdown(time.Second) })
	return d, store, created.ID
}

func testJob(listenerID int64, url, secret string) DeliveryJob {
	return DeliveryJob{
		ListenerID: listenerID, ListenerName: "test", URL: url, Secret: secret,
		DeliveryID: newDeliveryID(), Event: "message", MessageID: "3EB0TEST", ChatJID: opsGroup,
		Body: []byte(`{"version":1,"message":{"content":"secret text"}}`), CreatedAt: time.Now(),
	}
}

type deliveryRow struct {
	status, event string
	attempts      int
	code          *int
	errText       *string
}

func waitForDelivery(t *testing.T, store *MessageStore, deliveryID string) deliveryRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var r deliveryRow
		err := store.db.QueryRow(`SELECT status, event, attempts, status_code, error FROM listener_deliveries WHERE delivery_id = ?`,
			deliveryID).Scan(&r.status, &r.event, &r.attempts, &r.code, &r.errText)
		if err == nil {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery %s was never recorded", deliveryID)
	return deliveryRow{}
}

func TestDeliveryHeadersAndSignature(t *testing.T) {
	recv := newReceiver(t)
	d, store, id := newTestDeliverer(t, 8, 2, 2*time.Second)
	job := testJob(id, recv.srv.URL+"/hook", testSecret)
	d.Enqueue(job)

	if row := waitForDelivery(t, store, job.DeliveryID); row.status != "delivered" || row.attempts != 1 {
		t.Fatalf("row = %+v", row)
	}
	req := recv.requests[0]
	for header, want := range map[string]string{
		"Content-Type": "application/json", "User-Agent": webhookUserAgent,
		"X-Webhook-Event": "message", "X-Webhook-Delivery": job.DeliveryID,
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	ts := req.Header.Get("X-Webhook-Timestamp")
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Fatalf("timestamp %q", ts)
	}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(ts + "."))
	mac.Write(recv.bodies[0])
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); req.Header.Get("X-Webhook-Signature") != want {
		t.Errorf("signature = %q, want %q", req.Header.Get("X-Webhook-Signature"), want)
	}
}

func TestSignatureMatchesSpecExample(t *testing.T) {
	body := []byte(`{"version":1}`)
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte("1791331445." + string(body)))
	if got, want := signature(testSecret, 1791331445, body), "sha256="+hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("signature = %s, want %s", got, want)
	}
}

func TestNoSignatureWithoutSecret(t *testing.T) {
	recv := newReceiver(t)
	d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
	job := testJob(id, recv.srv.URL, "")
	d.Enqueue(job)
	waitForDelivery(t, store, job.DeliveryID)
	if sig := recv.requests[0].Header.Get("X-Webhook-Signature"); sig != "" {
		t.Errorf("signature sent without a secret: %q", sig)
	}
}

func TestRetryOutcomes(t *testing.T) {
	t.Run("503 then 200", func(t *testing.T) {
		recv := newReceiver(t, 503, 200)
		d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
		job := testJob(id, recv.srv.URL, testSecret)
		d.Enqueue(job)
		row := waitForDelivery(t, store, job.DeliveryID)
		if row.status != "delivered" || row.attempts != 2 {
			t.Fatalf("row = %+v", row)
		}
		if recv.requests[0].Header.Get("X-Webhook-Delivery") != recv.requests[1].Header.Get("X-Webhook-Delivery") {
			t.Error("delivery ID changed between attempts")
		}
	})

	t.Run("404 is not retried", func(t *testing.T) {
		recv := newReceiver(t, 404)
		d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
		job := testJob(id, recv.srv.URL, "")
		d.Enqueue(job)
		row := waitForDelivery(t, store, job.DeliveryID)
		if row.status != "failed" || row.attempts != 1 || row.code == nil || *row.code != 404 {
			t.Fatalf("row = %+v", row)
		}
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		target := newReceiver(t)
		recv := newReceiver(t, 302)
		d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
		job := testJob(id, recv.srv.URL, "")
		d.Enqueue(job)
		if row := waitForDelivery(t, store, job.DeliveryID); row.status != "failed" || row.attempts != 1 {
			t.Fatalf("row = %+v", row)
		}
		if target.count() != 0 {
			t.Error("redirect target was contacted")
		}
	})

	t.Run("hanging receiver fails after the timeout", func(t *testing.T) {
		recv := newReceiver(t)
		recv.delay = 5 * time.Second
		d, store, id := newTestDeliverer(t, 8, 1, 100*time.Millisecond)
		job := testJob(id, recv.srv.URL, "")
		d.Enqueue(job)
		row := waitForDelivery(t, store, job.DeliveryID)
		if row.status != "failed" || row.attempts != maxDeliveryAttempts || row.errText == nil || *row.errText != "timeout" {
			t.Fatalf("row = %+v", row)
		}
	})
}

func TestDeliveryLogHoldsNoContentAndIsPruned(t *testing.T) {
	recv := newReceiver(t)
	d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
	job := testJob(id, recv.srv.URL+"/hook?token=abc", testSecret)
	d.Enqueue(job)
	waitForDelivery(t, store, job.DeliveryID)

	var columns []string
	rows, _ := store.db.Query("SELECT name FROM pragma_table_info('listener_deliveries')")
	for rows.Next() {
		var c string
		rows.Scan(&c)
		columns = append(columns, c)
	}
	rows.Close()
	for _, c := range columns {
		if c == "payload" || c == "content" || c == "url" || c == "secret" || c == "signature" {
			t.Errorf("delivery log has a %s column", c)
		}
	}

	for i := 0; i < deliveryLogLimit+50; i++ {
		store.db.Exec(`INSERT INTO listener_deliveries (listener_id, delivery_id, event, status) VALUES (?, ?, 'message', 'delivered')`,
			id, "bulk"+strconv.Itoa(i))
	}
	d.prune(true)
	var n int
	var newest string
	store.db.QueryRow("SELECT COUNT(*) FROM listener_deliveries").Scan(&n)
	store.db.QueryRow("SELECT delivery_id FROM listener_deliveries ORDER BY id DESC LIMIT 1").Scan(&newest)
	if n != deliveryLogLimit || newest != "bulk"+strconv.Itoa(deliveryLogLimit+49) {
		t.Errorf("after pruning: %d rows, newest %s", n, newest)
	}
}

func TestShutdownRespectsDeadline(t *testing.T) {
	recv := newReceiver(t)
	recv.delay = 10 * time.Second
	d, _, id := newTestDeliverer(t, 8, 1, 30*time.Second)
	d.Enqueue(testJob(id, recv.srv.URL, ""))
	time.Sleep(50 * time.Millisecond) // let the worker start the attempt
	start := time.Now()
	d.Shutdown(200 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Shutdown took %v", elapsed)
	}
	if d.Enqueue(testJob(id, recv.srv.URL, "")) {
		t.Error("Enqueue accepted a job after Shutdown")
	}
}

func TestBoundedAsynchronousDelivery(t *testing.T) {
	recv := newReceiver(t)
	recv.delay = 10 * time.Second
	d, store, id := newTestDeliverer(t, 18, 2, 30*time.Second)

	r := NewListenerRegistry(15*time.Minute, time.Now)
	r.Reload(store)
	var accepted atomic.Int32
	start := time.Now()
	for i := 0; i < 21; i++ {
		m := msgIn(opsGroup, ana, "guardia")
		m.ID = "BURST" + strconv.Itoa(i)
		for _, job := range r.Evaluate(m) {
			job.URL = recv.srv.URL
			if d.Enqueue(job) {
				accepted.Add(1)
			}
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("evaluating and enqueueing 21 matches took %v", elapsed)
	}
	// 2 workers take one job each, 18 wait in the queue, the 21st is dropped
	time.Sleep(100 * time.Millisecond)
	if got := accepted.Load(); got != 20 {
		t.Errorf("accepted %d jobs, want 20", got)
	}
	if d.drops.Load() != 1 {
		t.Errorf("drops = %d, want 1", d.drops.Load())
	}
	d.flushDrops()
	var dropped int
	store.db.QueryRow("SELECT COUNT(*) FROM listener_deliveries WHERE status = 'dropped'").Scan(&dropped)
	if dropped != 1 {
		t.Errorf("dropped rows = %d, want 1", dropped)
	}
	_ = id
}

func TestRetryIntoFullQueueIsDropped(t *testing.T) {
	recv := newReceiver(t, 503, 503, 503, 503)
	d, store, id := newTestDeliverer(t, 1, 1, 2*time.Second)
	d.backoff = []time.Duration{50 * time.Millisecond}
	failing := testJob(id, recv.srv.URL, "")
	d.Enqueue(failing)
	time.Sleep(20 * time.Millisecond) // first attempt done, retry pending

	// Fill the queue so the retry finds no room: block the worker on a slow receiver
	slow := newReceiver(t)
	slow.delay = 2 * time.Second
	d.Enqueue(testJob(id, slow.srv.URL, ""))
	time.Sleep(10 * time.Millisecond)
	d.Enqueue(testJob(id, slow.srv.URL, ""))

	time.Sleep(150 * time.Millisecond)
	d.flushDrops()
	row := waitForDelivery(t, store, failing.DeliveryID)
	if row.status != "dropped" {
		t.Errorf("retry into a full queue: %+v", row)
	}
}

func TestDeliverIsSynchronousAndRecorded(t *testing.T) {
	recv := newReceiver(t)
	d, store, id := newTestDeliverer(t, 8, 1, 2*time.Second)
	job := testJob(id, recv.srv.URL, testSecret)
	job.Event = "test"
	if o := d.Deliver(job); o.status != "delivered" || o.statusCode != 200 {
		t.Fatalf("outcome = %+v", o)
	}
	if row := waitForDelivery(t, store, job.DeliveryID); row.event != "test" || row.status != "delivered" {
		t.Errorf("row = %+v", row)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	o := d.Deliver(testJob(id, closedURL, ""))
	if o.status != "failed" || o.err == "" {
		t.Errorf("closed port outcome = %+v", o)
	}
}
