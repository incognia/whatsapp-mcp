package main

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

var testSelf = netip.MustParseAddrPort("127.0.0.1:8080")

func TestValidateWebhookURL(t *testing.T) {
	policy := urlPolicy{self: testSelf}
	allow := urlPolicy{self: testSelf, allowedHosts: parseAllowedHosts("n8n.example.org, 127.0.0.1, *.hooks.example.com")}

	cases := []struct {
		name    string
		url     string
		policy  urlPolicy
		wantErr string
	}{
		{"file scheme", "file:///etc/passwd", policy, "http or https"},
		{"gopher scheme", "gopher://example.com", policy, "http or https"},
		{"plain http public", "http://hooks.example.com/wa", policy, "https for public hosts"},
		{"https public refused by default", "https://hooks.example.com/wa", policy, "WEBHOOK_ALLOWED_HOSTS"},
		{"host name refused by default", "https://n8n.home.example/hook", policy, "WEBHOOK_ALLOWED_HOSTS"},
		{"public ip refused by default", "https://93.184.216.34/hook", policy, "WEBHOOK_ALLOWED_HOSTS"},
		{"https public listed", "https://hooks.example.com/wa", urlPolicy{self: testSelf, allowedHosts: []string{"hooks.example.com"}}, ""},
		{"local automation", "http://127.0.0.1:5678/webhook/wa", policy, ""},
		{"localhost", "http://localhost:5678/hook", policy, ""},
		{"private network", "http://192.168.100.20/hook", policy, ""},
		{"ipv6 unique local", "http://[fd00::1]:8000/hook", policy, ""},
		{"bridge itself", "http://127.0.0.1:8080/api/send", policy, "bridge's own API"},
		{"bridge via localhost", "http://localhost:8080/api/send", policy, "bridge's own API"},
		{"bridge via other loopback", "http://127.0.0.2:8080/api/send", policy, "bridge's own API"},
		{"credentials", "https://user:pass@hooks.example.com/wa", allow, "user name or password"},
		{"no host", "https:///wa", policy, "must have a host"},
		{"metadata literal", "http://169.254.169.254/latest", policy, "link-local"},
		{"too long", "https://hooks.example.com/" + strings.Repeat("a", maxWebhookURLLength), allow, "longer than"},
		{"empty", "", policy, "required"},
		{"allowlist hit", "https://n8n.example.org/hook", allow, ""},
		{"allowlist suffix", "https://a.hooks.example.com/hook", allow, ""},
		{"allowlist ip", "http://127.0.0.1:5678/hook", allow, ""},
		{"allowlist miss", "https://other.example.net/hook", allow, "WEBHOOK_ALLOWED_HOSTS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookURL(tc.url, tc.policy)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestMaskURLAndLogTarget(t *testing.T) {
	if got := maskURL("https://ntfy.example.org/alerts?auth=abc123"); got != "https://ntfy.example.org/alerts?auth=***" {
		t.Errorf("maskURL = %q", got)
	}
	if got := maskURL("http://127.0.0.1:5678/webhook/wa"); got != "http://127.0.0.1:5678/webhook/wa" {
		t.Errorf("maskURL without query = %q", got)
	}
	got := logTarget("https://hooks.example.org/T000/B000/XXXX?token=abc")
	if got != "https://hooks.example.org" {
		t.Errorf("logTarget = %q", got)
	}
}

func TestForbiddenIP(t *testing.T) {
	cases := []struct {
		ip          string
		port        int
		allowPublic bool
		forbidden   bool
	}{
		{"169.254.169.254", 80, true, true},
		{"fe80::1", 80, true, true},
		{"0.0.0.0", 80, true, true},
		{"224.0.0.1", 80, true, true},
		{"255.255.255.255", 80, true, true},
		{"127.0.0.1", 8080, true, true}, // the bridge itself
		{"127.0.0.1", 5678, false, false},
		{"192.168.1.10", 8080, false, false},
		{"fd00::1", 443, false, false},
		{"93.184.216.34", 443, false, true}, // public, no WEBHOOK_ALLOWED_HOSTS
		{"93.184.216.34", 443, true, false}, // public, allowlist set
		{"::ffff:93.184.216.34", 443, false, true},
	}
	for _, tc := range cases {
		if got := forbiddenIP(net.ParseIP(tc.ip), tc.port, testSelf, tc.allowPublic); got != tc.forbidden {
			t.Errorf("forbiddenIP(%s:%d) = %v, want %v", tc.ip, tc.port, got, tc.forbidden)
		}
	}
}

func TestDialControlRefusesMetadataBeforeConnecting(t *testing.T) {
	client := newWebhookHTTPClient(2*time.Second, urlPolicy{self: testSelf})
	start := time.Now()
	_, err := client.Post("http://169.254.169.254/latest/meta-data", "application/json", nil)
	if err == nil || !strings.Contains(err.Error(), "refused by the webhook URL policy") {
		t.Fatalf("err = %v, want a policy refusal", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("refusal took %v; it should happen before any network wait", time.Since(start))
	}
	_ = http.StatusOK
}
