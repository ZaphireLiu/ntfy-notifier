package luaengine

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]map[string]any
}

func (k *memKV) KV(topic string) map[string]any {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := map[string]any{}
	for key, v := range k.m[topic] {
		out[key] = v
	}
	return out
}

func (k *memKV) SetKV(topic string, kv map[string]any) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[topic] = kv
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHandleSMS(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "sms.lua", `
function on_message(m)
  local d = ntfy.json.decode(m.message)
  if not d or not d.vcode then return nil end
  return {
    title = "验证码 " .. d.vcode,
    body = d.name,
    priority = 4,
    on_click = { copy = d.vcode, sensitive = true },
    buttons = { { label = "复制", copy = d.vcode } },
  }
end
`)
	kv := &memKV{m: map[string]map[string]any{}}
	eng := New([]config.Topic{{Name: "sms"}}, dir, 100*time.Millisecond, kv, logx.Nop())
	defer eng.Close()

	msg := model.Message{
		Topic:    "sms",
		ID:       "id1",
		Message:  `{"vcode":"123456","name":"示例公司","sent_by":"1068","timestamp":"2026-01-01 12:00:00"}`,
		Priority: 4,
	}
	spec, handled, err := eng.Handle("sms", msg)
	if err != nil || !handled || spec == nil {
		t.Fatalf("handled=%v err=%v spec=%v", handled, err, spec)
	}
	if spec.Title != "验证码 123456" || spec.Body != "示例公司" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.OnClick == nil || spec.OnClick.Type != model.ActionCopy || spec.OnClick.Text != "123456" || !spec.OnClick.Sensitive {
		t.Fatalf("on_click = %+v", spec.OnClick)
	}
	if len(spec.Buttons) != 1 || spec.Buttons[0].Label != "复制" {
		t.Fatalf("buttons = %+v", spec.Buttons)
	}

	// A non-JSON message is swallowed by this script.
	spec, handled, err = eng.Handle("sms", model.Message{Topic: "sms", ID: "id2", Message: "plain"})
	if err != nil || !handled || spec != nil {
		t.Fatalf("expected suppressed, got handled=%v spec=%+v err=%v", handled, spec, err)
	}
}

func TestSandboxRemovesDangerousLibs(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "t.lua", `
function on_message(m)
  return { title = tostring(io), body = tostring(os.execute) }
end
`)
	eng := New([]config.Topic{{Name: "t"}}, dir, 100*time.Millisecond, nil, logx.Nop())
	defer eng.Close()
	spec, handled, err := eng.Handle("t", model.Message{Topic: "t", Message: "x"})
	if err != nil || !handled || spec == nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if spec.Title != "nil" || spec.Body != "nil" {
		t.Fatalf("sandbox leak: title=%q body=%q", spec.Title, spec.Body)
	}
}

func TestTimeoutPoisonsVM(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "slow.lua", `
function on_message(m)
  while true do end
end
`)
	eng := New([]config.Topic{{Name: "slow"}}, dir, 60*time.Millisecond, nil, logx.Nop())
	defer eng.Close()

	done := make(chan error, 1)
	go func() {
		_, _, err := eng.Handle("slow", model.Message{Topic: "slow", Message: "x"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("script did not time out")
	}
}

func TestReloadAndKV(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "kv.lua", `
function setup(ctx)
  KV = ctx.kv
  KV.count = KV.count or 0
end
function on_message(m)
  KV.count = KV.count + 1
  return { title = "v1", body = tostring(KV.count) }
end
`)
	kv := &memKV{m: map[string]map[string]any{}}
	eng := New([]config.Topic{{Name: "kv"}}, dir, 100*time.Millisecond, kv, logx.Nop())
	defer eng.Close()

	spec, _, err := eng.Handle("kv", model.Message{Topic: "kv", Message: "x"})
	if err != nil || spec.Body != "1" {
		t.Fatalf("first call: spec=%+v err=%v", spec, err)
	}
	spec, _, err = eng.Handle("kv", model.Message{Topic: "kv", Message: "x"})
	if err != nil || spec.Body != "2" {
		t.Fatalf("second call: spec=%+v err=%v", spec, err)
	}
	if got := kv.KV("kv")["count"]; got != float64(2) {
		t.Fatalf("persisted kv = %#v", got)
	}

	writeScript(t, dir, "kv.lua", `
function setup(ctx) KV = ctx.kv end
function on_message(m) return { title = "v2", body = tostring(KV.count) } end
`)
	if err := eng.Reload("kv"); err != nil {
		t.Fatal(err)
	}
	spec, _, err = eng.Handle("kv", model.Message{Topic: "kv", Message: "x"})
	if err != nil || spec.Title != "v2" || spec.Body != "2" {
		t.Fatalf("after reload: spec=%+v err=%v", spec, err)
	}
}

func TestMissingScriptIsNotHandled(t *testing.T) {
	eng := New([]config.Topic{{Name: "none"}}, t.TempDir(), 100*time.Millisecond, nil, logx.Nop())
	defer eng.Close()
	spec, handled, err := eng.Handle("none", model.Message{Topic: "none", Message: "x"})
	if err != nil || handled || spec != nil {
		t.Fatalf("handled=%v spec=%+v err=%v", handled, spec, err)
	}
}
