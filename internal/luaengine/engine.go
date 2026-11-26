package luaengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/gopher-lua"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// KVStore persists per-topic script state.
type KVStore interface {
	KV(topic string) map[string]any
	SetKV(topic string, kv map[string]any)
}

// ErrTimeout is returned when a script exceeds its execution budget.
var ErrTimeout = errors.New("lua script timed out")

// Engine owns one Lua VM per topic. VMs are serialized: Handle and Reload for
// the same topic never run concurrently.
type Engine struct {
	timeout time.Duration
	kv      KVStore
	log     *slog.Logger

	mu    sync.RWMutex
	vms   map[string]*topicVM
	paths map[string]string

	errMu sync.Mutex
	errs  map[string]string
}

type topicVM struct {
	mu     sync.Mutex
	topic  string
	path   string
	script *script
}

type script struct {
	state   *lua.LState
	fn      *lua.LFunction
	kv      *lua.LTable
	path    string
	timeout time.Duration
}

// New creates the engine and loads every configured script that exists.
func New(topics []config.Topic, dir string, timeout time.Duration, kv KVStore, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	e := &Engine{
		timeout: timeout,
		kv:      kv,
		log:     log,
		vms:     make(map[string]*topicVM, len(topics)),
		paths:   make(map[string]string, len(topics)),
		errs:    make(map[string]string),
	}
	for _, t := range topics {
		if !t.IsEnabled() {
			continue
		}
		e.vms[t.Name] = &topicVM{topic: t.Name, path: t.ScriptPath(dir)}
		e.paths[t.Name] = t.ScriptPath(dir)
	}
	e.loadAll()
	return e
}

// Handle runs the topic's script for one message.
//
// handled=false means "no script (or the script failed); use the default
// mapping". handled=true with spec=nil means the script deliberately
// suppressed the notification.
func (e *Engine) Handle(topic string, m model.Message) (spec *model.Spec, handled bool, err error) {
	e.mu.RLock()
	vm := e.vms[topic]
	e.mu.RUnlock()
	if vm == nil {
		return nil, false, nil
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.script == nil {
		if !fileExists(vm.path) {
			return nil, false, nil
		}
		if err := e.loadScriptLocked(vm); err != nil {
			return nil, false, err
		}
	}
	s := vm.script
	out, err := s.call(m)
	if err != nil {
		if errors.Is(err, ErrTimeout) {
			// A timed-out VM is considered poisoned.
			s.close()
			vm.script = nil
			e.setError(topic, err)
			return nil, false, fmt.Errorf("script %s: %w", vm.topic, err)
		}
		e.setError(topic, err)
		return nil, false, fmt.Errorf("script %s: %w", vm.topic, err)
	}
	e.clearError(topic)
	if e.kv != nil {
		e.kv.SetKV(topic, luaTableToMap(s.kv))
	}
	if out == nil {
		return nil, true, nil
	}
	return out, true, nil
}

// Reload re-reads a topic's script. On compile failure the previous script is
// kept.
func (e *Engine) Reload(topic string) error {
	e.mu.RLock()
	vm := e.vms[topic]
	e.mu.RUnlock()
	if vm == nil {
		return fmt.Errorf("unknown topic %q", topic)
	}
	vm.mu.Lock()
	defer vm.mu.Unlock()
	old := vm.script
	newScript, err := e.build(vm.topic, vm.path)
	if err != nil {
		e.setError(topic, err)
		return err
	}
	vm.script = newScript
	if old != nil {
		old.close()
	}
	e.clearError(topic)
	return nil
}

// ReloadAll re-reads every script and detaches scripts whose file disappeared.
func (e *Engine) ReloadAll() {
	e.mu.RLock()
	vms := make([]*topicVM, 0, len(e.vms))
	for _, vm := range e.vms {
		vms = append(vms, vm)
	}
	e.mu.RUnlock()

	for _, vm := range vms {
		vm.mu.Lock()
		old := vm.script
		if !fileExists(vm.path) {
			vm.script = nil
			vm.mu.Unlock()
			if old != nil {
				old.close()
			}
			e.clearError(vm.topic)
			continue
		}
		newScript, err := e.build(vm.topic, vm.path)
		if err == nil {
			vm.script = newScript
			e.clearError(vm.topic)
		} else {
			e.setError(vm.topic, err)
		}
		vm.mu.Unlock()
		if err == nil && old != nil {
			old.close()
		}
	}
}

func (e *Engine) loadAll() {
	e.mu.RLock()
	vms := make([]*topicVM, 0, len(e.vms))
	for _, vm := range e.vms {
		vms = append(vms, vm)
	}
	e.mu.RUnlock()
	for _, vm := range vms {
		vm.mu.Lock()
		if fileExists(vm.path) {
			s, err := e.build(vm.topic, vm.path)
			if err != nil {
				e.setError(vm.topic, err)
			} else {
				vm.script = s
				e.clearError(vm.topic)
			}
		}
		vm.mu.Unlock()
	}
}

func (e *Engine) loadScriptLocked(vm *topicVM) error {
	s, err := e.build(vm.topic, vm.path)
	if err != nil {
		e.setError(vm.topic, err)
		return err
	}
	vm.script = s
	e.clearError(vm.topic)
	return nil
}

func (e *Engine) build(topic, path string) (*script, error) {
	L := newSandbox(topic, e.log)
	s := &script{state: L, path: path, timeout: e.timeout}
	s.kv = e.kvTable(L, topic)

	loadCtx, loadCancel := context.WithTimeout(context.Background(), e.timeout)
	defer loadCancel()
	L.SetContext(loadCtx)
	err := L.DoFile(path)
	L.RemoveContext()
	if err != nil {
		L.Close()
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	fn, ok := L.GetGlobal("on_message").(*lua.LFunction)
	if !ok {
		L.Close()
		return nil, fmt.Errorf("%s: on_message function is missing", path)
	}
	s.fn = fn

	setup, ok := L.GetGlobal("setup").(*lua.LFunction)
	if ok {
		ctxTable := L.NewTable()
		L.SetField(ctxTable, "kv", s.kv)
		L.SetField(ctxTable, "topic", lua.LString(topic))
		cctx, cancel := context.WithTimeout(context.Background(), e.timeout)
		defer cancel()
		L.SetContext(cctx)
		err := L.CallByParam(lua.P{Fn: setup, NRet: 0, Protect: true}, ctxTable)
		L.RemoveContext()
		if err != nil {
			L.Close()
			return nil, fmt.Errorf("%s: setup: %w", path, err)
		}
	}
	return s, nil
}

func (e *Engine) kvTable(L *lua.LState, topic string) *lua.LTable {
	t := L.NewTable()
	if e.kv == nil {
		return t
	}
	for k, v := range e.kv.KV(topic) {
		t.RawSetString(k, anyToLValue(L, v))
	}
	return t
}

func (s *script) call(m model.Message) (*model.Spec, error) {
	L := s.state
	msg := messageToTable(L, m)

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	L.SetContext(ctx)
	err := L.CallByParam(lua.P{Fn: s.fn, NRet: 1, Protect: true}, msg)
	L.RemoveContext()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrTimeout
		}
		return nil, err
	}
	ret := L.Get(-1)
	L.Pop(1)
	switch ret.Type() {
	case lua.LTNil:
		return nil, nil
	case lua.LTBool:
		if !lua.LVAsBool(ret) {
			return nil, nil
		}
		return &model.Spec{}, nil
	case lua.LTString:
		return &model.Spec{Body: ret.String()}, nil
	case lua.LTTable:
		return specFromTable(ret.(*lua.LTable)), nil
	default:
		return nil, fmt.Errorf("on_message returned %s, want nil/table/string", ret.Type())
	}
}

func (s *script) close() {
	if s.state != nil {
		s.state.Close()
		s.state = nil
		s.fn = nil
	}
}

// StartWatcher reloads scripts on filesystem changes until ctx is cancelled.
func (e *Engine) StartWatcher(ctx context.Context, dir string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return err
	}
	go func() {
		defer w.Close()
		var timer *time.Timer
		reload := func() {
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(300*time.Millisecond, e.ReloadAll)
		}
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if strings.HasSuffix(strings.ToLower(ev.Name), ".lua") {
					reload()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				e.log.Warn("script watcher error", "err", err)
			}
		}
	}()
	return nil
}

func (e *Engine) Errors() map[string]string {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	out := make(map[string]string, len(e.errs))
	for k, v := range e.errs {
		out[k] = v
	}
	return out
}

func (e *Engine) SetError(topic string, err error) { e.setError(topic, err) }

func (e *Engine) setError(topic string, err error) {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	if err == nil {
		delete(e.errs, topic)
		return
	}
	e.errs[topic] = err.Error()
}

func (e *Engine) clearError(topic string) { e.setError(topic, nil) }

func (e *Engine) Close() {
	e.mu.RLock()
	vms := make([]*topicVM, 0, len(e.vms))
	for _, vm := range e.vms {
		vms = append(vms, vm)
	}
	e.mu.RUnlock()
	for _, vm := range vms {
		vm.mu.Lock()
		if vm.script != nil {
			vm.script.close()
			vm.script = nil
		}
		vm.mu.Unlock()
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
