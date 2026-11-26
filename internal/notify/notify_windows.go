//go:build windows

package notify

import (
	"context"
	"log/slog"
	"sync"

	toast "github.com/mtunjic/winrt-toast"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

type windowsNotifier struct {
	appID string
	log   *slog.Logger
	mu    sync.Mutex
}

// New registers the AUMID and returns a Windows toast notifier.
func New(appID, appName string, log *slog.Logger) (Notifier, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := toast.RegisterAppID(appID, appName); err != nil {
		return nil, err
	}
	return &windowsNotifier{appID: appID, log: log}, nil
}

func (w *windowsNotifier) Notify(_ context.Context, n model.Notification) error {
	tn := toast.Notification{
		AppID:               w.appID,
		Title:               n.Title,
		Message:             n.Body,
		Icon:                n.Icon,
		ActivationArguments: n.Launch,
		Audio:               audioFor(n.Sound),
		Duration:            durationFor(n.Duration),
	}
	for _, a := range n.Actions {
		tn.Actions = append(tn.Actions, toast.Action{Label: a.Label, Arguments: a.Launch})
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if err := toast.Push(tn); err != nil {
		w.log.Warn("toast push failed", "err", err)
		return err
	}
	return nil
}

func (w *windowsNotifier) Close() error { return nil }

// Unregister removes the per-user AUMID registration.
func Unregister(appID string) error {
	return toast.UnregisterAppID(appID)
}

func audioFor(sound string) toast.Audio {
	switch sound {
	case "silent":
		return toast.AudioSilent
	case "mail":
		return toast.AudioMail
	default:
		return toast.AudioDefault
	}
}

func durationFor(d string) toast.Duration {
	if d == "long" {
		return toast.DurationLong
	}
	return toast.DurationShort
}
