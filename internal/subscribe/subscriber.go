package subscribe

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// State is the subset of the store needed by the subscriber.
type State interface {
	Since(topics []string) int64
	LastID(topic string) string
	MarkSeen(topic string, ts int64, id string)
}

// Status describes the connection state for the tray and logs.
type Status int

const (
	StatusStopped Status = iota
	StatusConnecting
	StatusConnected
	StatusUnauthorized
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusConnecting:
		return "connecting"
	case StatusConnected:
		return "connected"
	case StatusUnauthorized:
		return "unauthorized"
	case StatusError:
		return "error"
	default:
		return "stopped"
	}
}

// Options configures a Subscriber.
type Options struct {
	Server    config.Server
	Topics    []string
	State     State
	Logger    *slog.Logger
	OnMessage func(model.Message)
	OnStatus  func(Status, error)
	// OnStale is called for replayed messages that are older than MaxAge.
	OnStale func(model.Message, time.Duration)
	// Watchdog disconnects a stream that has been silent for this long.
	// Defaults to 90s.
	Watchdog time.Duration
	// MaxAge drops messages older than this, which happens when a reconnect
	// replays the server cache. Zero disables the check.
	MaxAge time.Duration
}

type Subscriber struct {
	opts   Options
	client *http.Client
	log    *slog.Logger
}

func New(opts Options) (*Subscriber, error) {
	if len(opts.Topics) == 0 {
		return nil, errors.New("subscriber: no topics")
	}
	if opts.OnMessage == nil {
		return nil, errors.New("subscriber: OnMessage is required")
	}
	if opts.Watchdog <= 0 {
		opts.Watchdog = 90 * time.Second
	}
	if opts.State == nil {
		opts.State = noopState{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	client, err := newHTTPClient(opts.Server)
	if err != nil {
		return nil, err
	}
	return &Subscriber{opts: opts, client: client, log: opts.Logger}, nil
}

func newHTTPClient(cfg config.Server) (*http.Client, error) {
	tr := &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Proxy)) {
	case "", "none":
		// Explicitly no proxy.
	case "env":
		tr.Proxy = http.ProxyFromEnvironment
	default:
		u, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("server.proxy: %w", err)
		}
		tr.Proxy = http.ProxyURL(u)
	}

	if cfg.TLSCA != "" || cfg.InsecureSkipVerify {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in for debugging
		if cfg.TLSCA != "" {
			pem, err := os.ReadFile(cfg.TLSCA)
			if err != nil {
				return nil, fmt.Errorf("server.tls_ca: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("server.tls_ca: no certificates found in %s", cfg.TLSCA)
			}
			tlsCfg.RootCAs = pool
		}
		tr.TLSClientConfig = tlsCfg
	}
	return &http.Client{Transport: tr}, nil
}

// Run blocks until ctx is cancelled, reconnecting with exponential backoff.
func (s *Subscriber) Run(ctx context.Context) {
	if s.opts.Server.Mode == "poll" {
		s.runPoll(ctx)
		return
	}
	s.runStream(ctx)
}

func (s *Subscriber) runStream(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			s.status(StatusStopped, nil)
			return
		}
		s.status(StatusConnecting, nil)
		connected, err := s.streamOnce(ctx)
		if ctx.Err() != nil {
			s.status(StatusStopped, nil)
			return
		}
		if connected {
			backoff = time.Second
		}
		delay := backoff
		if isAuthError(err) {
			s.status(StatusUnauthorized, err)
			delay = 5 * time.Minute
		} else {
			s.status(StatusError, err)
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
		if err != nil {
			s.log.Warn("subscription disconnected", "topics", s.opts.Topics, "retry_in", delay, "err", err)
		}
		if !sleepCtx(ctx, jitter(delay)) {
			s.status(StatusStopped, nil)
			return
		}
	}
}

func (s *Subscriber) streamOnce(ctx context.Context) (bool, error) {
	since := s.sinceString(false)
	u := s.buildURL(since, false)
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(connCtx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	s.authorize(req)

	watchdog := time.AfterFunc(s.opts.Watchdog, cancel)
	defer watchdog.Stop()

	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	connected := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		watchdog.Reset(s.opts.Watchdog)
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var m model.Message
		if err := json.Unmarshal(line, &m); err != nil {
			s.log.Warn("invalid ndjson line", "err", err)
			continue
		}
		m.Raw = string(line)
		switch m.Event {
		case model.EventOpen:
			connected = true
			s.status(StatusConnected, nil)
		case model.EventMessage:
			if m.Priority <= 0 {
				m.Priority = 3
			}
			if !s.observe(&m) {
				continue
			}
			s.opts.OnMessage(m)
		case model.EventKeepalive:
			// watchdog already reset
		default:
			// message_delete / message_clear / poll_request
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return connected, ctx.Err()
		}
		return connected, fmt.Errorf("stream read: %w", err)
	}
	return connected, io.EOF
}

func (s *Subscriber) runPoll(ctx context.Context) {
	backoff := 5 * time.Second
	since := s.sinceString(true)
	for {
		if ctx.Err() != nil {
			s.status(StatusStopped, nil)
			return
		}
		s.status(StatusConnecting, nil)
		start, err := s.pollOnce(ctx, since)
		if err != nil {
			if ctx.Err() != nil {
				s.status(StatusStopped, nil)
				return
			}
			if isAuthError(err) {
				s.status(StatusUnauthorized, err)
			} else {
				s.status(StatusError, err)
			}
			s.log.Warn("poll failed", "retry_in", backoff, "err", err)
			if !sleepCtx(ctx, jitter(backoff)) {
				s.status(StatusStopped, nil)
				return
			}
			backoff *= 2
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			continue
		}
		// Advance the cursor to the start of this request minus a generous
		// overlap. The overlap covers messages that landed while the request
		// was in flight and tolerates clock skew between us and the server;
		// deduplication removes the repeats.
		next := start - 60
		if next > 0 {
			since = strconv.FormatInt(next, 10)
		}
		backoff = 5 * time.Second
		s.status(StatusConnected, nil)
		if !sleepCtx(ctx, s.opts.Server.PollInterval.Std()) {
			s.status(StatusStopped, nil)
			return
		}
	}
}

func (s *Subscriber) pollOnce(ctx context.Context, since string) (start int64, err error) {
	start = time.Now().Unix()
	u := s.buildURL(since, true)
	timeout := 2 * s.opts.Server.PollInterval.Std()
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return start, err
	}
	s.authorize(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return start, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return start, &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var m model.Message
		if err := json.Unmarshal(line, &m); err != nil {
			s.log.Warn("invalid ndjson line", "err", err)
			continue
		}
		if m.Event != model.EventMessage {
			continue
		}
		m.Raw = string(line)
		if m.Priority <= 0 {
			m.Priority = 3
		}
		if !s.observe(&m) {
			continue
		}
		s.opts.OnMessage(m)
	}
	return start, scanner.Err()
}

// observe advances the replay cursor and reports whether the message is worth
// notifying. The cursor is advanced before the age check so that a stale
// message is not replayed again on the next reconnect.
func (s *Subscriber) observe(m *model.Message) bool {
	now := time.Now()
	if m.Time > now.Unix() {
		// Clamp timestamps from a server whose clock is ahead; otherwise the
		// cursor would jump into the future and skip real messages.
		m.Time = now.Unix()
	}
	if m.Topic != "" && m.Time > 0 {
		s.opts.State.MarkSeen(m.Topic, m.Time, m.ID)
	}
	if s.opts.MaxAge <= 0 || m.Time <= 0 {
		return true
	}
	age := now.Sub(time.Unix(m.Time, 0))
	if age <= s.opts.MaxAge {
		return true
	}
	if s.opts.OnStale != nil {
		s.opts.OnStale(*m, age)
	}
	return false
}

func (s *Subscriber) buildURL(since string, poll bool) string {
	base := strings.TrimRight(s.opts.Server.BaseURL, "/")
	u := base + "/" + strings.Join(s.opts.Topics, ",") + "/json"
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if poll {
		q.Set("poll", "1")
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// sinceString computes the replay cursor. On a first run without any stored
// state it returns config.initial_since, or "now" in poll mode so the whole
// cache is not replayed as a burst of toasts.
func (s *Subscriber) sinceString(poll bool) string {
	var ts int64
	if s.opts.State != nil {
		ts = s.opts.State.Since(s.opts.Topics)
	}
	if ts > 0 {
		if now := time.Now().Unix(); ts > now {
			ts = now
		}
		return strconv.FormatInt(ts, 10)
	}
	if v := strings.TrimSpace(s.opts.Server.InitialSince); v != "" {
		return v
	}
	if poll {
		return strconv.FormatInt(time.Now().Unix()-2, 10)
	}
	return ""
}

func (s *Subscriber) authorize(req *http.Request) {
	if s.opts.Server.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.Server.Token)
	} else if s.opts.Server.Username != "" {
		req.SetBasicAuth(s.opts.Server.Username, s.opts.Server.Password)
	}
}

func (s *Subscriber) status(st Status, err error) {
	if s.opts.OnStatus != nil {
		s.opts.OnStatus(st, err)
	}
}

type noopState struct{}

func (noopState) Since([]string) int64           { return 0 }
func (noopState) LastID(string) string           { return "" }
func (noopState) MarkSeen(string, int64, string) {}

// HTTPError is returned for non-200 responses.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("http %d", e.StatusCode)
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}

func isAuthError(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.StatusCode == http.StatusUnauthorized || he.StatusCode == http.StatusForbidden
	}
	return false
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 0.8 + rand.Float64()*0.4 //nolint:gosec // timing jitter only
	return time.Duration(float64(d) * factor)
}
