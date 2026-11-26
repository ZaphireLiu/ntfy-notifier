package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// New creates a logger writing to a rotating file, optionally teeing to stderr.
func New(logPath string, debug, console bool) (*slog.Logger, func() error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		// Fall back to stderr below.
		logPath = ""
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	var w io.Writer = os.Stderr
	var closeFn = func() error { return nil }
	if logPath != "" {
		lj := &lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    5,
			MaxBackups: 3,
			MaxAge:     14,
			Compress:   true,
		}
		if console {
			w = io.MultiWriter(lj, os.Stderr)
		} else {
			w = lj
		}
		closeFn = lj.Close
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h), closeFn
}

func Nop() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
