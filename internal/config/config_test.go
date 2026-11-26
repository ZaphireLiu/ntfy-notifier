package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com/"
token = "tk_test"

[[topic]]
name = "sms"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.BaseURL != "https://ntfy.example.com" {
		t.Fatalf("base_url not normalized: %q", cfg.Server.BaseURL)
	}
	if cfg.App.Scheme != "ntfy-notifier" {
		t.Fatalf("default scheme: %q", cfg.App.Scheme)
	}
	if cfg.Notify.MaxPerMin != 10 || cfg.Notify.LongBodyLimit != 300 {
		t.Fatalf("defaults not applied: %+v", cfg.Notify)
	}
	if cfg.Notify.MaxMessageAge.Std() != 5*time.Minute {
		t.Fatalf("max_message_age default: %v", cfg.Notify.MaxMessageAge.Std())
	}
	if cfg.Scripts.Timeout.Std() != 200*time.Millisecond {
		t.Fatalf("timeout default: %v", cfg.Scripts.Timeout.Std())
	}
	if got := cfg.Topics[0].ScriptPath("/s"); got != "/s/sms.lua" {
		t.Fatalf("script path: %q", got)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com"
token = "x"
typo_key = true

[[topic]]
name = "sms"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestInvalidTopicName(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com"
token = "x"

[[topic]]
name = "bad/topic"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid topic error")
	}
}

func TestCaseCollision(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com"
token = "x"

[[topic]]
name = "SMS"

[[topic]]
name = "sms"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected case collision error")
	}
}

func TestQuietHours(t *testing.T) {
	q, err := ParseQuietHours("23:30-08:00")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time {
		return time.Date(2026, 11, 25, h, m, 0, 0, time.Local)
	}
	cases := []struct {
		h, m int
		want bool
	}{
		{23, 0, false},
		{23, 45, true},
		{0, 30, true},
		{7, 59, true},
		{8, 0, false},
		{12, 0, false},
	}
	for _, c := range cases {
		if got := q.Contains(at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d = %v, want %v", c.h, c.m, got, c.want)
		}
	}
	if _, err := ParseQuietHours("25:00-08:00"); err == nil {
		t.Fatal("expected invalid hour error")
	}
	if _, err := ParseQuietHours("08:00-08:00"); err == nil {
		t.Fatal("expected identical range error")
	}
}

func TestMaxMessageAge(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com"
token = "x"

[notify]
max_message_age = "90s"

[[topic]]
name = "sms"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Notify.MaxMessageAge.Std(); got != 90*time.Second {
		t.Fatalf("max_message_age = %v", got)
	}
}

func TestNegativeMaxMessageAgeRejected(t *testing.T) {
	path := writeConfig(t, `
[server]
base_url = "https://ntfy.example.com"
token = "x"

[notify]
max_message_age = "-1s"

[[topic]]
name = "sms"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected negative max_message_age error")
	}
}

func TestTopicLimit(t *testing.T) {
	zero := 0
	cfg := Default()
	cfg.Topics = []Topic{{Name: "sms", MaxPerMin: &zero}}
	if got := cfg.Topics[0].MaxPerMin; got == nil || *got != 0 {
		t.Fatal("explicit zero limit lost")
	}
}
