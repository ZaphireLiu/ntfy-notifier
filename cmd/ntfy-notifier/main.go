package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZaphireLiu/ntfy-notifier/internal/action"
	"github.com/ZaphireLiu/ntfy-notifier/internal/activate"
	"github.com/ZaphireLiu/ntfy-notifier/internal/app"
	"github.com/ZaphireLiu/ntfy-notifier/internal/autostart"
	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/logx"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
	"github.com/ZaphireLiu/ntfy-notifier/internal/notify"
	"github.com/ZaphireLiu/ntfy-notifier/internal/paths"
	"github.com/ZaphireLiu/ntfy-notifier/internal/singleinstance"
	"github.com/ZaphireLiu/ntfy-notifier/internal/tray"
)

var version = "dev"

//go:embed icon.ico
var trayIcon []byte

func main() {
	cfgPath := flag.String("config", paths.DefaultConfigPath(), "path to config.toml")
	console := flag.Bool("console", false, "also log to stderr")
	debug := flag.Bool("debug", false, "enable debug logging")
	testToast := flag.Bool("test-toast", false, "send a test toast and exit")
	testMessage := flag.String("test-message", "", "process one JSON message file and exit")
	activateURI := flag.String("activate", "", "handle a toast activation URI (internal)")
	unregister := flag.Bool("unregister", false, "remove URI scheme and autostart entries")
	showVersion := flag.Bool("version", false, "print version and exit")
	autostartFlag := flag.Bool("autostart", false, "set when started at login (internal)")
	flag.Parse()
	_ = autostartFlag

	if *showVersion {
		fmt.Printf("ntfy-notifier %s\n", version)
		return
	}

	log, closeLog := logx.New(paths.LogFile(), *debug, *console)
	defer closeLog()
	slog.SetDefault(log)

	exePath, err := os.Executable()
	if err != nil {
		log.Error("cannot determine executable path", "err", err)
		os.Exit(1)
	}

	if *unregister {
		runUnregister(*cfgPath, exePath, log)
		return
	}
	if *activateURI != "" {
		runActivate(*activateURI, *cfgPath, log)
		return
	}

	lock, already, err := singleinstance.Acquire(`Local\ntfy-notifier`)
	if err != nil {
		log.Warn("single instance check failed", "err", err)
	} else if already {
		log.Info("another instance is already running")
		return
	}
	if lock != nil {
		defer lock.Release()
	}

	a, err := app.New(*cfgPath, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}

	if err := activate.RegisterScheme(a.Scheme(), exePath); err != nil {
		log.Warn("could not register URI scheme; toast click actions will not work", "err", err)
	}
	if a.AutostartEnabled() || autostart.Enabled("") {
		// Refresh the command line so a moved executable keeps working, even
		// when autostart was toggled from the tray rather than the config.
		if err := autostart.Set(true, exePath); err != nil {
			log.Warn("could not enable autostart", "err", err)
		}
	}

	if *testToast {
		if err := a.TestToast(); err != nil {
			log.Error("test toast failed", "err", err)
			os.Exit(1)
		}
		a.Stop()
		return
	}
	if *testMessage != "" {
		if err := runTestMessage(a, *testMessage); err != nil {
			log.Error("test message failed", "err", err)
			os.Exit(1)
		}
		a.Stop()
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := a.Start(ctx); err != nil {
		log.Error("start failed", "err", err)
		os.Exit(1)
	}

	tray.Run(ctx, a, tray.Actions{
		Pause:        a.Pause,
		Resume:       a.Resume,
		Reload:       a.ReloadScripts,
		OpenConfig:   func() { openPath(a.ConfigDir(), log) },
		OpenScripts:  func() { openPath(a.ScriptDir(), log) },
		OpenLogs:     func() { openPath(a.LogDir(), log) },
		Autostart:    autostart.Enabled(exePath),
		SetAutostart: func(v bool) error { return autostart.Set(v, exePath) },
		Quit:         cancel,
	}, trayIcon)

	cancel()
	a.Stop()
	log.Info("stopped")
}

func runUnregister(cfgPath, exePath string, log *slog.Logger) {
	scheme := "ntfy-notifier"
	appID := "com.example.ntfynotifier"
	if cfg, err := config.Load(cfgPath); err == nil {
		scheme = cfg.App.Scheme
		appID = cfg.App.AppID
	}
	if err := notify.Unregister(appID); err != nil {
		log.Warn("unregister app id failed", "err", err)
	}
	if err := activate.UnregisterScheme(scheme); err != nil {
		log.Warn("unregister scheme failed", "err", err)
	}
	if err := autostart.Set(false, exePath); err != nil {
		log.Warn("disable autostart failed", "err", err)
	}
	log.Info("unregistered", "scheme", scheme)
}

func runActivate(raw, cfgPath string, log *slog.Logger) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("activation failed: config", "err", err)
		return
	}
	pending, err := activate.NewPending(paths.PendingDir(), cfg.Actions.PendingTTL.Std(), log)
	if err != nil {
		log.Error("activation failed: pending", "err", err)
		return
	}
	ex := action.New(cfg.Actions, log)
	if err := activate.Handle(context.Background(), raw, cfg.App.Scheme, pending, ex, log); err != nil {
		log.Warn("activation failed", "err", err)
	}
}

func runTestMessage(a *app.App, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m model.Message
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if m.Event == "" {
		m.Event = model.EventMessage
	}
	if m.Topic == "" {
		return fmt.Errorf("message has no topic")
	}
	if m.Priority <= 0 {
		m.Priority = 3
	}
	a.HandleMessage(m)
	return nil
}

func openPath(path string, log *slog.Logger) {
	if path == "" {
		return
	}
	if err := action.OpenPath(path); err != nil {
		log.Warn("open path failed", "path", path, "err", err)
	}
}
