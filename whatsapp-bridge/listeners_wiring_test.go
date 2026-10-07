package main

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// History sync stores messages but must never reach the listener registry
func TestHistorySyncNeverFiresListeners(t *testing.T) {
	recv := newReceiver(t)
	store := openTestStore(t)
	l := validListener()
	l.Contains = []string{"december"}
	l.WebhookURL = recv.srv.URL
	if _, err := store.CreateListener(l); err != nil {
		t.Fatal(err)
	}

	prevRegistry, prevDeliverer := listenerRegistry, webhookDeliverer
	t.Cleanup(func() { listenerRegistry, webhookDeliverer = prevRegistry, prevDeliverer })
	listenerRegistry = NewListenerRegistry(0, time.Now)
	listenerRegistry.Reload(store)
	webhookDeliverer = NewDeliverer(store, waLog.Noop, 8, 1, time.Second, testSelf)
	t.Cleanup(func() { webhookDeliverer.Shutdown(time.Second) })

	now := time.Now()
	data := onDemandSync(waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
		historyMsg("H1", "december", now.Add(-time.Minute)))
	processHistorySync(store, data, testHistoryDeps(), nil, waLog.Noop)

	var stored, deliveries int
	store.db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = 'H1'").Scan(&stored)
	time.Sleep(100 * time.Millisecond)
	store.db.QueryRow("SELECT COUNT(*) FROM listener_deliveries").Scan(&deliveries)
	if stored != 1 || deliveries != 0 || recv.count() != 0 {
		t.Errorf("stored=%d deliveries=%d requests=%d, want 1/0/0", stored, deliveries, recv.count())
	}
}
