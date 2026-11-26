package activate

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// URI builds the protocol URI embedded in a toast.
func URI(scheme, id string) string {
	return scheme + "://act?id=" + id
}

// ParseURI extracts the pending action id from an activation URI.
func ParseURI(raw, scheme string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, scheme) {
		return "", fmt.Errorf("unexpected scheme %q", u.Scheme)
	}
	if u.Host != "act" {
		return "", fmt.Errorf("unexpected activation host %q", u.Host)
	}
	id := u.Query().Get("id")
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid action id")
	}
	return id, nil
}

// Executor is the subset of action.Executor used here.
type Executor interface {
	Execute(ctx context.Context, a model.Action) error
}

// Handle resolves an activation URI, runs the action and reports errors.
func Handle(ctx context.Context, raw, scheme string, pending *Pending, ex Executor, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	id, err := ParseURI(raw, scheme)
	if err != nil {
		return err
	}
	a, err := pending.Take(id)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ex.Execute(cctx, *a); err != nil {
		log.Warn("toast action failed", "type", a.Type, "err", err)
		return err
	}
	log.Info("toast action executed", "type", a.Type)
	return nil
}
