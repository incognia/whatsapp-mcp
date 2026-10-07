package main

// Webhook delivery: a bounded queue fed by the WhatsApp event path, drained by a fixed pool of
// workers, with timer-based retries so no worker ever sleeps.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

const (
	maxDeliveryAttempts = 4
	maxRetryAfter       = 60 * time.Second
	maxResponseRead     = 4096
	deliveryLogLimit    = 1000
	maintenanceInterval = time.Minute
	webhookUserAgent    = "whatsapp-bridge-webhook/1"
)

// defaultBackoff is the wait before the 2nd, 3rd and 4th attempts
var defaultBackoff = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

// newWebhookHTTPClient builds the client used for every webhook attempt: bounded time, no
// redirects, no environment proxy (it would bypass the IP check) and the dial-time IP policy
func newWebhookHTTPClient(timeout time.Duration, self netip.AddrPort) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Control: dialControl(self)}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}

// signature returns the X-Webhook-Signature value for a timestamp and body
func signature(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// deliveryOutcome is the result of one attempt
type deliveryOutcome struct {
	status     string // delivered, failed or retry
	statusCode int
	err        string
	retryAfter time.Duration
}

// Deliverer sends queued delivery jobs to their webhooks
type Deliverer struct {
	jobs    chan DeliveryJob
	client  *http.Client
	store   *MessageStore
	logger  waLog.Logger
	backoff []time.Duration
	now     func() time.Time

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.RWMutex
	closed  bool
	timers  map[*time.Timer]bool
	workers sync.WaitGroup
	stop    chan struct{}

	drops      atomic.Int64
	dropMu     sync.Mutex
	droppedLog []DeliveryJob

	pruneMu   sync.Mutex
	lastPrune time.Time
}

// NewDeliverer starts the worker pool and the once-a-minute maintenance goroutine
func NewDeliverer(store *MessageStore, logger waLog.Logger, queueSize, workers int, timeout time.Duration, self netip.AddrPort) *Deliverer {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Deliverer{
		jobs:    make(chan DeliveryJob, queueSize),
		client:  newWebhookHTTPClient(timeout, self),
		store:   store,
		logger:  logger,
		backoff: defaultBackoff,
		now:     time.Now,
		ctx:     ctx,
		cancel:  cancel,
		timers:  map[*time.Timer]bool{},
		stop:    make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		d.workers.Add(1)
		go d.worker()
	}
	go d.maintenance()
	return d
}

// Enqueue queues a job without ever blocking; a full queue drops it
func (d *Deliverer) Enqueue(job DeliveryJob) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return false
	}
	select {
	case d.jobs <- job:
		return true
	default:
		d.noteDrop(job)
		return false
	}
}

// noteDrop counts a dropped job; the maintenance goroutine logs and records it later, so the
// event path never touches SQLite
func (d *Deliverer) noteDrop(job DeliveryJob) {
	d.drops.Add(1)
	d.dropMu.Lock()
	if len(d.droppedLog) < cap(d.jobs)+deliveryLogLimit {
		d.droppedLog = append(d.droppedLog, job)
	}
	d.dropMu.Unlock()
}

func (d *Deliverer) worker() {
	defer d.workers.Done()
	for job := range d.jobs {
		job.Attempt++
		outcome := d.attempt(job)
		switch {
		case outcome.status == "retry" && job.Attempt < maxDeliveryAttempts:
			d.scheduleRetry(job, outcome.retryAfter)
		case outcome.status == "retry":
			outcome.status = "failed"
			d.finish(job, outcome)
		default:
			d.finish(job, outcome)
		}
	}
}

// attempt makes one signed POST and classifies the answer
func (d *Deliverer) attempt(job DeliveryJob) deliveryOutcome {
	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, job.URL, bytes.NewReader(job.Body))
	if err != nil {
		return deliveryOutcome{status: "failed", err: "invalid request"}
	}
	ts := d.now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webhookUserAgent)
	req.Header.Set("X-Webhook-Event", job.Event)
	req.Header.Set("X-Webhook-Delivery", job.DeliveryID)
	req.Header.Set("X-Webhook-Timestamp", strconv.FormatInt(ts, 10))
	if job.Secret != "" {
		req.Header.Set("X-Webhook-Signature", signature(job.Secret, ts, job.Body))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return deliveryOutcome{status: "retry", err: describeDeliveryError(err)}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseRead))

	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return deliveryOutcome{status: "delivered", statusCode: code}
	case code >= 300 && code < 400:
		return deliveryOutcome{status: "failed", statusCode: code, err: "redirect not followed"}
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return deliveryOutcome{status: "retry", statusCode: code, err: http.StatusText(code), retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	default:
		return deliveryOutcome{status: "failed", statusCode: code, err: http.StatusText(code)}
	}
}

// describeDeliveryError keeps the cause but drops the URL, whose path and query may hold secrets
func describeDeliveryError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func parseRetryAfter(value string) time.Duration {
	if s, err := strconv.Atoi(value); err == nil && s >= 0 {
		if d := time.Duration(s) * time.Second; d <= maxRetryAfter {
			return d
		}
	}
	return 0
}

// scheduleRetry re-enqueues a job after its backoff without occupying a worker
func (d *Deliverer) scheduleRetry(job DeliveryJob, retryAfter time.Duration) {
	delay := d.backoff[min(job.Attempt-1, len(d.backoff)-1)]
	delay += time.Duration(rand.Int63n(int64(delay)/5 + 1)) // up to 20% jitter
	if retryAfter > delay {
		delay = retryAfter
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		d.mu.Lock()
		delete(d.timers, timer)
		closed := d.closed
		if !closed {
			select {
			case d.jobs <- job:
			default:
				d.noteDrop(job)
			}
		}
		d.mu.Unlock()
	})
	d.timers[timer] = true
}

// finish records the outcome and logs failures with scheme and host only
func (d *Deliverer) finish(job DeliveryJob, o deliveryOutcome) {
	if o.status == "failed" {
		d.logger.Warnf("Webhook delivery %s for listener %d (%s) to %s failed after %d attempt(s): %s",
			job.DeliveryID, job.ListenerID, job.ListenerName, logTarget(job.URL), job.Attempt, o.err)
	}
	d.record(job, o.status, job.Attempt, o.statusCode, o.err)
}

// record writes one delivery log row (never payload, content, secret or URL) and prunes
func (d *Deliverer) record(job DeliveryJob, status string, attempts, statusCode int, errText string) {
	var code interface{}
	if statusCode != 0 {
		code = statusCode
	}
	var errValue interface{}
	if errText != "" {
		errValue = errText
	}
	_, err := d.store.db.Exec(`INSERT INTO listener_deliveries
		(listener_id, delivery_id, event, message_id, chat_jid, status, attempts, status_code, error, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ListenerID, job.DeliveryID, job.Event, job.MessageID, job.ChatJID, status, attempts, code, errValue,
		job.CreatedAt, d.now())
	if err != nil {
		// The listener may have been deleted meanwhile; nothing else to do
		return
	}
	d.prune(false)
}

// prune keeps the newest deliveryLogLimit rows, at most once a minute unless forced
func (d *Deliverer) prune(force bool) {
	d.pruneMu.Lock()
	defer d.pruneMu.Unlock()
	if !force && d.now().Sub(d.lastPrune) < maintenanceInterval {
		return
	}
	d.lastPrune = d.now()
	d.store.db.Exec(`DELETE FROM listener_deliveries WHERE id <= (
		SELECT id FROM listener_deliveries ORDER BY id DESC LIMIT 1 OFFSET ?)`, deliveryLogLimit)
}

// flushDrops logs the drop count and records dropped jobs
func (d *Deliverer) flushDrops() {
	n := d.drops.Swap(0)
	d.dropMu.Lock()
	dropped := d.droppedLog
	d.droppedLog = nil
	d.dropMu.Unlock()
	if n == 0 && len(dropped) == 0 {
		return
	}
	if n > 0 {
		d.logger.Warnf("Dropped %d webhook deliveries in the last minute because the queue was full", n)
	}
	for _, job := range dropped {
		d.record(job, "dropped", job.Attempt, 0, "queue full")
	}
}

func (d *Deliverer) maintenance() {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.flushDrops()
		case <-d.stop:
			return
		}
	}
}

// Deliver makes one synchronous, unretried attempt (test deliveries) and records it
func (d *Deliverer) Deliver(job DeliveryJob) deliveryOutcome {
	job.Attempt = 1
	o := d.attempt(job)
	if o.status == "retry" {
		o.status = "failed"
	}
	d.record(job, o.status, 1, o.statusCode, o.err)
	return o
}

// Shutdown stops intake and retries, then waits for in-flight attempts up to timeout
func (d *Deliverer) Shutdown(timeout time.Duration) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	for timer := range d.timers {
		timer.Stop()
	}
	d.timers = nil
	close(d.jobs)
	d.mu.Unlock()
	close(d.stop)

	done := make(chan struct{})
	go func() {
		d.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		d.logger.Warnf("Webhook delivery shutdown timed out after %v; remaining deliveries dropped", timeout)
	}
	d.cancel()
	d.flushDrops()
}

// String helps logs show the job without its body or secret
func (job DeliveryJob) String() string {
	return fmt.Sprintf("delivery %s (listener %d, %s)", job.DeliveryID, job.ListenerID, logTarget(job.URL))
}
