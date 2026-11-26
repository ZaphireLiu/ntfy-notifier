package action

import (
	"context"
	"testing"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

func newTestExecutor(allowRun bool) *Executor {
	return New(config.Actions{
		AllowOpen:      []string{"https"},
		AllowHTTP:      true,
		HTTPAllowHosts: []string{"api.example.com", "*.internal.example.com"},
		AllowRun:       allowRun,
	}, logx.Nop())
}

func TestCopyAction(t *testing.T) {
	ex := newTestExecutor(false)
	var got string
	var sensitive bool
	ex.clipboard = func(text string, s bool) error {
		got, sensitive = text, s
		return nil
	}
	a := model.Action{Type: model.ActionCopy, Text: "123456", Sensitive: true}
	if err := ex.Execute(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if got != "123456" || !sensitive {
		t.Fatalf("clipboard got %q sensitive=%v", got, sensitive)
	}
	if err := ex.Validate(model.Action{Type: model.ActionCopy}); err == nil {
		t.Fatal("empty copy should be rejected")
	}
}

func TestOpenSchemeAllowlist(t *testing.T) {
	ex := newTestExecutor(false)
	if err := ex.Validate(model.Action{Type: model.ActionOpen, URL: "http://example.com"}); err == nil {
		t.Fatal("http should be rejected when only https is allowed")
	}
	if err := ex.Validate(model.Action{Type: model.ActionOpen, URL: "https://example.com"}); err != nil {
		t.Fatalf("https rejected: %v", err)
	}
	if err := ex.Validate(model.Action{Type: model.ActionOpen, URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("file scheme should be rejected")
	}
}

func TestHTTPActionValidation(t *testing.T) {
	ex := newTestExecutor(false)
	ok := model.Action{Type: model.ActionHTTP, Method: "POST", URL: "https://api.example.com/hook", Body: "{}"}
	if err := ex.Validate(ok); err != nil {
		t.Fatalf("valid http rejected: %v", err)
	}
	badHost := ok
	badHost.URL = "https://evil.example.org/hook"
	if err := ex.Validate(badHost); err == nil {
		t.Fatal("host outside allowlist accepted")
	}
	wild := ok
	wild.URL = "https://svc.internal.example.com/hook"
	if err := ex.Validate(wild); err != nil {
		t.Fatalf("wildcard host rejected: %v", err)
	}
	badMethod := ok
	badMethod.Method = "OPTIONS"
	if err := ex.Validate(badMethod); err == nil {
		t.Fatal("OPTIONS accepted")
	}
}

func TestRunDisabled(t *testing.T) {
	ex := newTestExecutor(false)
	a := model.Action{Type: model.ActionRun, Cmd: "calc.exe"}
	if err := ex.Validate(a); err == nil {
		t.Fatal("run should be disabled")
	}
	ex2 := newTestExecutor(true)
	if err := ex2.Validate(a); err != nil {
		t.Fatalf("run should be allowed: %v", err)
	}
}

func TestHostAllowed(t *testing.T) {
	if !hostAllowed("API.example.com", []string{"api.example.com"}) {
		t.Fatal("exact host mismatch")
	}
	if !hostAllowed("a.internal.example.com", []string{"*.internal.example.com"}) {
		t.Fatal("wildcard host mismatch")
	}
	if hostAllowed("internal.example.com", []string{"*.internal.example.com"}) {
		t.Fatal("wildcard must not match the bare domain")
	}
}
