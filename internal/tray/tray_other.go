//go:build !windows

package tray

import (
	"context"
	"time"
)

// Provider supplies status text for the tray.
type Provider interface {
	StatusText() string
	StatusDetail() string
	Recent() []string
}

type Actions struct {
	Pause        func(time.Duration)
	Resume       func()
	Reload       func()
	OpenConfig   func()
	OpenScripts  func()
	OpenLogs     func()
	Autostart    bool
	SetAutostart func(bool) error
	Quit         func()
}

// Run has no tray on non-Windows platforms; it waits for cancellation.
func Run(ctx context.Context, _ Provider, _ Actions, _ []byte) {
	<-ctx.Done()
}
