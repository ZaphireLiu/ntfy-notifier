//go:build !windows

package notify

import "log/slog"

// New falls back to console output on non-Windows platforms.
func New(_, _ string, log *slog.Logger) (Notifier, error) {
	return NewConsole(log), nil
}

func Unregister(string) error { return nil }
