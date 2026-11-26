package action

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// Executor validates and runs actions triggered from a toast.
type Executor struct {
	cfg       config.Actions
	log       *slog.Logger
	client    *http.Client
	clipboard func(text string, sensitive bool) error
}

func New(cfg config.Actions, log *slog.Logger) *Executor {
	if log == nil {
		log = slog.Default()
	}
	tr := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Executor{
		cfg:       cfg,
		log:       log,
		client:    &http.Client{Transport: tr},
		clipboard: writeClipboard,
	}
}

// Validate rejects malformed or disallowed actions. It is called before an
// action is written to the pending directory and again before execution.
func (e *Executor) Validate(a model.Action) error {
	switch a.Type {
	case model.ActionCopy:
		if strings.TrimSpace(a.Text) == "" {
			return errors.New("copy: text is empty")
		}
		if len(a.Text) > 64*1024 {
			return errors.New("copy: text is too large")
		}
	case model.ActionOpen:
		if _, err := e.openURL(a.URL); err != nil {
			return err
		}
	case model.ActionHTTP:
		if !e.cfg.AllowHTTP {
			return errors.New("http: disabled by config")
		}
		u, err := url.Parse(a.URL)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("http: scheme %q is not allowed", u.Scheme)
		}
		if !hostAllowed(u.Hostname(), e.cfg.HTTPAllowHosts) {
			return fmt.Errorf("http: host %q is not allowed", u.Hostname())
		}
		switch strings.ToUpper(a.Method) {
		case "", http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return fmt.Errorf("http: method %q is not allowed", a.Method)
		}
		if len(a.Body) > 64*1024 {
			return errors.New("http: body is too large")
		}
		if a.Timeout < 0 || a.Timeout > 30*time.Second {
			return fmt.Errorf("http: timeout must be between 0 and 30s")
		}
	case model.ActionRun:
		if !e.cfg.AllowRun {
			return errors.New("run: disabled by config")
		}
		if strings.TrimSpace(a.Cmd) == "" {
			return errors.New("run: cmd is empty")
		}
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	return nil
}

func (e *Executor) Execute(ctx context.Context, a model.Action) error {
	if err := e.Validate(a); err != nil {
		return err
	}
	switch a.Type {
	case model.ActionCopy:
		return e.clipboard(a.Text, a.Sensitive)
	case model.ActionOpen:
		u, err := e.openURL(a.URL)
		if err != nil {
			return err
		}
		return openURL(u.String())
	case model.ActionHTTP:
		timeout := a.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		method := strings.ToUpper(a.Method)
		if method == "" {
			method = http.MethodPost
		}
		req, err := http.NewRequestWithContext(cctx, method, a.URL, strings.NewReader(a.Body))
		if err != nil {
			return err
		}
		for k, v := range a.Headers {
			req.Header.Set(k, v)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		if resp.StatusCode >= 400 {
			return fmt.Errorf("http: status %d", resp.StatusCode)
		}
		return nil
	case model.ActionRun:
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(rctx, a.Cmd, a.Args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("run %s: %w (output: %s)", a.Cmd, err, strings.TrimSpace(string(out)))
		}
		e.log.Info("run action finished", "cmd", a.Cmd)
		return nil
	}
	return fmt.Errorf("unknown action type %q", a.Type)
}

func (e *Executor) openURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	allowed := false
	for _, s := range e.cfg.AllowOpen {
		if s == scheme {
			allowed = true
			break
		}
	}
	if !allowed || u.Host == "" {
		return nil, fmt.Errorf("open: url %q is not allowed", raw)
	}
	return u, nil
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(host)
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		if strings.HasPrefix(entry, "*.") {
			suffix := entry[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && host != suffix[1:] {
				return true
			}
		}
	}
	return false
}
