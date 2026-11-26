package notify

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

// Notifier delivers a notification to the user.
type Notifier interface {
	Notify(ctx context.Context, n model.Notification) error
	Close() error
}

// Console prints notifications; used for development and non-Windows builds.
type Console struct {
	log *slog.Logger
	mu  sync.Mutex
}

func NewConsole(log *slog.Logger) *Console {
	if log == nil {
		log = slog.Default()
	}
	return &Console{log: log}
}

func (c *Console) Notify(_ context.Context, n model.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log.Info("toast",
		"title", n.Title,
		"body", n.Body,
		"sound", n.Sound,
		"duration", n.Duration,
		"launch", n.Launch,
		"buttons", len(n.Actions),
	)
	return nil
}

func (c *Console) Close() error { return nil }
