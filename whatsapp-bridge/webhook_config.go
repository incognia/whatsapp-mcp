package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Shared by the REST server and the WhatsApp event handler; nil until main sets them up
var (
	listenerRegistry *ListenerRegistry
	webhookDeliverer *Deliverer
)

// webhookConfig holds the WEBHOOK_* settings
type webhookConfig struct {
	allowedHosts []string
	adminToken   string
	queueSize    int
	workers      int
	timeout      time.Duration
	maxAge       time.Duration
}

// restBindAddr is the address the REST API listens on (BIND_ADDR, loopback by default)
func restBindAddr() string {
	if addr := os.Getenv("BIND_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1"
}

// envDuration reads a Go duration ("15m") or a number of seconds ("900")
func envDuration(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	if s, err := strconv.Atoi(raw); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
		return d
	}
	return def
}

func envInt(name string, def, minimum int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v >= minimum {
		return v
	}
	return def
}

func loadWebhookConfig() webhookConfig {
	return webhookConfig{
		allowedHosts: parseAllowedHosts(os.Getenv("WEBHOOK_ALLOWED_HOSTS")),
		adminToken:   os.Getenv("WEBHOOK_ADMIN_TOKEN"),
		queueSize:    envInt("WEBHOOK_QUEUE_SIZE", 256, 1),
		workers:      envInt("WEBHOOK_WORKERS", 2, 1),
		timeout:      envDuration("WEBHOOK_TIMEOUT", 10*time.Second),
		maxAge:       envDuration("WEBHOOK_MAX_AGE", 15*time.Minute),
	}
}

// String describes the settings for the start-up log, never including the token
func (c webhookConfig) String() string {
	allowed := "any"
	if len(c.allowedHosts) > 0 {
		allowed = strings.Join(c.allowedHosts, ",")
	}
	return fmt.Sprintf("workers=%d queue=%d timeout=%v max_age=%v allowed_hosts=%s admin_token=%v",
		c.workers, c.queueSize, c.timeout, c.maxAge, allowed, c.adminToken != "")
}

// selfAddrPort is the bridge's own API address, which webhooks must never target
func selfAddrPort(bindAddr string, port int) netip.AddrPort {
	addr, err := netip.ParseAddr(bindAddr)
	if err != nil {
		addr = netip.MustParseAddr("127.0.0.1")
	}
	return netip.AddrPortFrom(addr, uint16(port))
}

// clientLIDLookup maps LIDs through whatsmeow's LID store
func clientLIDLookup(client *whatsmeow.Client) lidLookup {
	return func(jid types.JID) (types.JID, bool) {
		pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid)
		if err != nil || pn.IsEmpty() {
			return types.EmptyJID, false
		}
		return pn, true
	}
}

// setupListeners creates the registry and the delivery workers and loads stored listeners
func setupListeners(store *MessageStore, cfg webhookConfig, port int, logger waLog.Logger) {
	bindAddr := restBindAddr()
	logger.Infof("Message listeners: %s", cfg)
	if cfg.adminToken == "" && !isLoopbackHost(bindAddr) {
		logger.Warnf("BIND_ADDR=%s is not loopback and WEBHOOK_ADMIN_TOKEN is not set: listener endpoints will refuse every request", bindAddr)
	}
	listenerRegistry = NewListenerRegistry(cfg.maxAge, time.Now)
	if err := listenerRegistry.Reload(store); err != nil {
		logger.Warnf("Failed to load message listeners: %v", err)
	}
	webhookDeliverer = NewDeliverer(store, logger, cfg.queueSize, cfg.workers, cfg.timeout, selfAddrPort(bindAddr, port))
}

// newListenerAPI wires the REST handlers to the running bridge
func newListenerAPI(client *whatsmeow.Client, store *MessageStore, cfg webhookConfig, port int) *listenerAPI {
	bindAddr := restBindAddr()
	return &listenerAPI{
		store: store, registry: listenerRegistry, deliverer: webhookDeliverer,
		policy:   urlPolicy{self: selfAddrPort(bindAddr, port), allowedHosts: cfg.allowedHosts},
		lidToPN:  clientLIDLookup(client),
		bindHost: bindAddr, adminToken: cfg.adminToken, now: time.Now,
	}
}

// notifyListeners evaluates a stored live message and queues its deliveries. It only reads
// memory, except for a contact-name lookup that whatsmeow serves from its cache, and does
// nothing at all when no listener exists.
func notifyListeners(client *whatsmeow.Client, msg *events.Message, chatName, content, mediaType, filename string) {
	if listenerRegistry == nil || webhookDeliverer == nil || len(listenerRegistry.Snapshot()) == 0 {
		return
	}
	var ownPN, ownLID types.JID
	if client.Store.ID != nil {
		ownPN = client.Store.ID.ToNonAD()
	}
	ownLID = client.Store.LID

	senderName := msg.Info.PushName
	if contact, err := client.Store.Contacts.GetContact(context.Background(), msg.Info.Sender); err == nil {
		switch {
		case contact.FullName != "":
			senderName = contact.FullName
		case contact.FirstName != "":
			senderName = contact.FirstName
		case senderName == "" && contact.PushName != "":
			senderName = contact.PushName
		}
	}

	m := IncomingMessage{
		ID:         msg.Info.ID,
		ChatJID:    msg.Info.Chat.String(),
		ChatName:   chatName,
		Sender:     msg.Info.Sender.User,
		SenderJID:  msg.Info.Sender.ToNonAD().String(),
		SenderName: senderName,
		Timestamp:  msg.Info.Timestamp,
		Content:    content,
		MediaType:  mediaType,
		Filename:   filename,
		IsFromMe:   msg.Info.IsFromMe,
		IsEdit:     msg.IsEdit,
		MentionsMe: mentionsMe(extractMentionedJIDs(msg.Message), ownPN, ownLID),
	}
	for _, job := range listenerRegistry.Evaluate(m) {
		webhookDeliverer.Enqueue(job)
	}
}
