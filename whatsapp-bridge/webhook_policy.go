package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"syscall"
)

const maxWebhookURLLength = 2048

// urlPolicy is what a webhook target is checked against, at save time and at dial time
type urlPolicy struct {
	// self is the bridge's own REST API address; a webhook must never point back at it
	self netip.AddrPort
	// allowedHosts, when non-empty, is the only set of hosts accepted (exact names or IPs, or
	// "*.suffix" entries), from WEBHOOK_ALLOWED_HOSTS
	allowedHosts []string
}

// parseAllowedHosts splits WEBHOOK_ALLOWED_HOSTS into normalised entries
func parseAllowedHosts(raw string) []string {
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func (p urlPolicy) hostAllowed(host string) bool {
	if len(p.allowedHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range p.allowedHosts {
		if suffix, ok := strings.CutPrefix(entry, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == entry {
			return true
		}
	}
	return false
}

// isLocalHost reports whether host is localhost or a loopback or private-network IP literal,
// the only targets allowed over plain http
func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback() || addr.IsPrivate()
}

// defaultPort returns the port a URL connects to
func defaultPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// pointsAtSelf reports whether host:port is the bridge's own API address
func (p urlPolicy) pointsAtSelf(host, port string) bool {
	if !p.self.IsValid() || port != fmt.Sprint(p.self.Port()) {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return p.self.Addr().IsLoopback() || p.self.Addr().IsUnspecified()
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	self := p.self.Addr().Unmap()
	if addr == self {
		return true
	}
	// A bridge bound to every interface or to loopback also answers on any loopback address
	return addr.IsLoopback() && (self.IsUnspecified() || self.IsLoopback())
}

// validateWebhookURL applies the save-time URL safety policy
func validateWebhookURL(raw string, p urlPolicy) error {
	if raw == "" {
		return errors.New("webhook_url is required")
	}
	if len(raw) > maxWebhookURLLength {
		return fmt.Errorf("webhook_url is longer than %d characters", maxWebhookURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("webhook_url is not a valid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("webhook_url must use http or https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("webhook_url must have a host")
	}
	if u.User != nil {
		return errors.New("webhook_url must not contain a user name or password")
	}
	if !p.hostAllowed(host) {
		return fmt.Errorf("webhook_url host %q is not in WEBHOOK_ALLOWED_HOSTS", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil && forbiddenAddr(addr) {
		return errors.New("webhook_url points at a link-local, multicast, broadcast or unspecified address")
	}
	if u.Scheme == "http" && !isLocalHost(host) {
		return errors.New("webhook_url must use https for public hosts (plain http is only allowed for localhost and private networks)")
	}
	// Without an allowlist only local targets are accepted, so a prompt-injected listener cannot
	// post messages to the internet
	if len(p.allowedHosts) == 0 && !isLocalHost(host) {
		return fmt.Errorf("webhook_url host %q is not local; list it in WEBHOOK_ALLOWED_HOSTS to allow it", host)
	}
	if p.pointsAtSelf(host, defaultPort(u)) {
		return errors.New("webhook_url must not point at the bridge's own API")
	}
	return nil
}

// forbiddenAddr reports addresses a webhook connection must never reach: link-local (including
// the 169.254.169.254 cloud metadata service), unspecified, multicast and broadcast
func forbiddenAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() ||
		addr.IsMulticast() || addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return true
	}
	return false
}

// forbiddenIP is forbiddenAddr for a resolved address and port, also refusing the bridge itself
// and, unless public targets are allowed (WEBHOOK_ALLOWED_HOSTS is set), any public address
func forbiddenIP(ip net.IP, port int, self netip.AddrPort, allowPublic bool) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if forbiddenAddr(addr) {
		return true
	}
	if !allowPublic && !addr.IsLoopback() && !addr.IsPrivate() {
		return true
	}
	if self.IsValid() && port == int(self.Port()) {
		s := self.Addr().Unmap()
		if addr == s || (addr.IsLoopback() && (s.IsUnspecified() || s.IsLoopback())) {
			return true
		}
	}
	return false
}

// dialControl rejects forbidden addresses after DNS resolution, so a host name that resolves (or
// later re-resolves) somewhere dangerous cannot bypass the save-time checks
func dialControl(p urlPolicy) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, portStr, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		var port int
		fmt.Sscan(portStr, &port)
		if forbiddenIP(net.ParseIP(host), port, p.self, len(p.allowedHosts) > 0) {
			return fmt.Errorf("connection to %s refused by the webhook URL policy", host)
		}
		return nil
	}
}

// maskURL hides query-string values (often tokens) for API answers
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	masked := make([]string, 0, len(q))
	for key := range q {
		masked = append(masked, url.QueryEscape(key)+"=***")
	}
	// Keep a stable order for readability
	sort.Strings(masked)
	u.RawQuery = strings.Join(masked, "&")
	return u.String()
}

// logTarget keeps only scheme and host, for logs
func logTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(invalid url)"
	}
	return u.Scheme + "://" + u.Host
}
