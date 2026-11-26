package subscribe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

type fakeState struct {
	mu     sync.Mutex
	seen   map[string]int64
	lastID map[string]string
}

func (f *fakeState) Since(topics []string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var min int64
	for _, t := range topics {
		if ts := f.seen[t]; ts > 0 && (min == 0 || ts < min) {
			min = ts
		}
	}
	if min > 5 {
		min -= 5
	}
	return min
}

func (f *fakeState) LastID(topic string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastID[topic]
}

func (f *fakeState) MarkSeen(topic string, ts int64, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ts > f.seen[topic] {
		f.seen[topic] = ts
	}
	if id != "" {
		if f.lastID == nil {
			f.lastID = map[string]string{}
		}
		f.lastID[topic] = id
	}
}

func TestStreamReceivesMessages(t *testing.T) {
	var gotSince, gotAuth string
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			gotSince = r.URL.Query().Get("since")
			gotAuth = r.Header.Get("Authorization")
		})
		if r.URL.Path != "/sms,agent/json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"id":"a1","time":100,"event":"open","topic":"sms,agent"}`)
		fmt.Fprintln(w, `{"id":"m1","time":101,"event":"message","topic":"sms","message":"hello","priority":4}`)
	}))
	defer srv.Close()

	state := &fakeState{seen: map[string]int64{"sms": 100}}
	msgs := make(chan model.Message, 4)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Token: "tk_test", Mode: "stream"},
		Topics:    []string{"sms", "agent"},
		State:     state,
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
		Watchdog:  2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sub.Run(ctx)
		close(done)
	}()

	select {
	case m := <-msgs:
		if m.Message != "hello" || m.Priority != 4 {
			t.Fatalf("unexpected message: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not stop")
	}

	if gotAuth != "Bearer tk_test" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotSince != "95" {
		t.Fatalf("since = %q, want 95", gotSince)
	}
	if state.seen["sms"] != 101 {
		t.Fatalf("lastSeen = %d", state.seen["sms"])
	}
}

func TestStaleMessageDroppedButCursorAdvanced(t *testing.T) {
	oldTime := time.Now().Add(-10 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"old1","time":%d,"event":"message","topic":"sms","message":"stale"}`+"\n", oldTime)
	}))
	defer srv.Close()

	state := &fakeState{seen: map[string]int64{}}
	msgs := make(chan model.Message, 1)
	stale := make(chan time.Duration, 1)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Mode: "stream"},
		Topics:    []string{"sms"},
		State:     state,
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
		OnStale:   func(_ model.Message, age time.Duration) { stale <- age },
		MaxAge:    5 * time.Minute,
		Watchdog:  2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sub.Run(ctx)
		close(done)
	}()

	select {
	case m := <-msgs:
		t.Fatalf("stale message delivered: %+v", m)
	case age := <-stale:
		if age < 9*time.Minute {
			t.Fatalf("age = %v", age)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stale callback")
	}

	state.mu.Lock()
	seen, id := state.seen["sms"], state.lastID["sms"]
	state.mu.Unlock()
	if seen != oldTime {
		t.Fatalf("lastSeen = %d, want %d (cursor must advance)", seen, oldTime)
	}
	if id != "old1" {
		t.Fatalf("lastID = %q", id)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not stop")
	}
}

func TestFutureTimestampClampedAndDelivered(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"f1","time":%d,"event":"message","topic":"sms","message":"hi"}`+"\n", future)
	}))
	defer srv.Close()

	state := &fakeState{seen: map[string]int64{}}
	msgs := make(chan model.Message, 1)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Mode: "stream"},
		Topics:    []string{"sms"},
		State:     state,
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
		MaxAge:    5 * time.Minute,
		Watchdog:  2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go sub.Run(ctx)
	defer cancel()

	select {
	case m := <-msgs:
		if m.Time > time.Now().Unix() {
			t.Fatalf("timestamp not clamped: %d", m.Time)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestSinceStringClampsFutureCursor(t *testing.T) {
	state := &fakeState{seen: map[string]int64{"sms": time.Now().Add(time.Hour).Unix()}}
	sub := &Subscriber{opts: Options{Topics: []string{"sms"}, State: state}}
	got, err := strconv.ParseInt(sub.sinceString(false), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if now := time.Now().Unix(); got > now {
		t.Fatalf("since = %d, want <= %d", got, now)
	}
}

func TestPollModeUsesTimestampOnFirstRun(t *testing.T) {
	var poll, since string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poll = r.URL.Query().Get("poll")
		since = r.URL.Query().Get("since")
		fmt.Fprintln(w, `{"id":"m2","time":200,"event":"message","topic":"agent","message":"ping"}`)
	}))
	defer srv.Close()

	msgs := make(chan model.Message, 2)
	sub, err := New(Options{
		Server:    config.Server{BaseURL: srv.URL, Mode: "poll", PollInterval: config.Duration(time.Hour)},
		Topics:    []string{"agent"},
		Logger:    logx.Nop(),
		OnMessage: func(m model.Message) { msgs <- m },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sub.Run(ctx)
		close(done)
	}()
	select {
	case <-msgs:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	cancel()
	<-done

	if poll != "1" {
		t.Fatalf("poll = %q", poll)
	}
	if since == "" {
		t.Fatal("since is empty")
	}
}
