package main

import (
	"strings"
	"testing"
)

func validCtx() validationContext {
	return validationContext{policy: urlPolicy{self: testSelf}, lidToPN: testLIDLookup, creating: true}
}

func validListener() Listener {
	l := newListenerDefaults()
	l.Name = "Guardias"
	l.Contains = []string{"guardia"}
	l.WebhookURL = testWebhook
	return l
}

func fieldsOf(errs []FieldError) []string {
	var fields []string
	for _, e := range errs {
		fields = append(fields, e.Field)
	}
	return fields
}

func TestValidateListenerValidAndDefaults(t *testing.T) {
	l := validListener()
	if errs := validateListener(&l, validCtx()); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	if !l.Enabled || l.MatchMode != "or" || l.MentionsMe || l.IncludeFromMe {
		t.Errorf("defaults = %+v", l)
	}
}

func TestValidateListenerRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Listener)
		ctx    func(*validationContext)
		field  string
	}{
		{"missing name", func(l *Listener) { l.Name = "  " }, nil, "name"},
		{"long name", func(l *Listener) { l.Name = strings.Repeat("a", 101) }, nil, "name"},
		{"no criteria", func(l *Listener) { l.Contains = nil }, nil, "criteria"},
		{"unknown match mode", func(l *Listener) { l.MatchMode = "xor" }, nil, "match_mode"},
		{"bad regex", func(l *Listener) { l.Regex = "([a-z" }, nil, "regex"},
		{"long regex", func(l *Listener) { l.Regex = strings.Repeat("a", 501) }, nil, "regex"},
		{"empty contains entry", func(l *Listener) { l.Contains = []string{" "} }, nil, "contains"},
		{"long contains entry", func(l *Listener) { l.Contains = []string{strings.Repeat("a", 201)} }, nil, "contains"},
		{"too many contains", func(l *Listener) { l.Contains = make([]string, 21); fill(l.Contains) }, nil, "contains"},
		{"too many chats", func(l *Listener) { l.ChatJIDs = make([]string, 51); fillNumbers(l.ChatJIDs) }, nil, "chat_jids"},
		{"bad sender", func(l *Listener) { l.Senders = []string{"ana"} }, nil, "senders"},
		{"bad url", func(l *Listener) { l.WebhookURL = "http://hooks.example.com/wa" }, nil, "webhook_url"},
		{"short secret", func(l *Listener) { l.Secret = "abc" }, nil, "secret"},
		{"listener cap", nil, func(c *validationContext) { c.existingCount = 50 }, "listeners"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := validListener()
			if tc.mutate != nil {
				tc.mutate(&l)
			}
			ctx := validCtx()
			if tc.ctx != nil {
				tc.ctx(&ctx)
			}
			errs := validateListener(&l, ctx)
			if got := fieldsOf(errs); len(got) != 1 || got[0] != tc.field {
				t.Errorf("fields = %v, want [%s] (%+v)", got, tc.field, errs)
			}
		})
	}
}

func TestValidateListenerReportsEveryProblem(t *testing.T) {
	l := validListener()
	l.Name = ""
	l.MatchMode = "xor"
	l.Regex = "([a-z"
	got := fieldsOf(validateListener(&l, validCtx()))
	want := []string{"name", "match_mode", "regex"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestValidateListenerNormalisesIdentities(t *testing.T) {
	l := validListener()
	l.Senders = []string{"+52 1 55 1234 5678", lidUser + "@lid"}
	l.ChatJIDs = []string{"5215512345678"}
	if errs := validateListener(&l, validCtx()); len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(l.Senders) != 1 || l.Senders[0] != "5215512345678" || l.ChatJIDs[0] != "5215512345678@s.whatsapp.net" {
		t.Errorf("normalised = %v / %v", l.Senders, l.ChatJIDs)
	}
}

func fill(values []string) {
	for i := range values {
		values[i] = "x"
	}
}

func fillNumbers(values []string) {
	for i := range values {
		values[i] = "52155000000" + string(rune('0'+i%10)) + string(rune('0'+i/10))
	}
}
