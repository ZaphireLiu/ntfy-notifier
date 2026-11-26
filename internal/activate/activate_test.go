package activate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

func TestPendingPutTake(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPending(dir, 10*time.Minute, logx.Nop())
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Put(model.Action{Type: model.ActionCopy, Text: "123456", Sensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("id length = %d", len(id))
	}
	a, err := p.Take(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != model.ActionCopy || a.Text != "123456" || !a.Sensitive {
		t.Fatalf("action = %+v", a)
	}
	if _, err := p.Take(id); err == nil {
		t.Fatal("action should be single use")
	}
	if _, err := p.Take("../etc/passwd"); err == nil {
		t.Fatal("invalid id accepted")
	}
}

func TestPendingExpiry(t *testing.T) {
	p, err := NewPending(t.TempDir(), 10*time.Millisecond, logx.Nop())
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Put(model.Action{Type: model.ActionCopy, Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := p.Take(id); err == nil {
		t.Fatal("expired action accepted")
	}
}

func TestParseURI(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	uri := URI("ntfy-notifier", id)
	got, err := ParseURI(uri, "ntfy-notifier")
	if err != nil || got != id {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := ParseURI(uri, "other"); err == nil {
		t.Fatal("wrong scheme accepted")
	}
	if _, err := ParseURI("ntfy-notifier://act?id=zzz", "ntfy-notifier"); err == nil {
		t.Fatal("invalid id accepted")
	}
	if _, err := ParseURI("https://example.com/?id="+id, "ntfy-notifier"); err == nil {
		t.Fatal("wrong scheme accepted")
	}
}

func TestCleanupRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPending(dir, time.Minute, logx.Nop())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	p.Cleanup()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("stale file was not removed")
	}
}
