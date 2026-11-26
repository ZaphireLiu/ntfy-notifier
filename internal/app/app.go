package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/ZaphireLiu/ntfy-notifier/internal/action"
	"github.com/ZaphireLiu/ntfy-notifier/internal/activate"
	"github.com/ZaphireLiu/ntfy-notifier/internal/config"
	"github.com/ZaphireLiu/ntfy-notifier/internal/luaengine"
	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
	"github.com/ZaphireLiu/ntfy-notifier/internal/notify"
	"github.com/ZaphireLiu/ntfy-notifier/internal/paths"
	"github.com/ZaphireLiu/ntfy-notifier/internal/router"
	"github.com/ZaphireLiu/ntfy-notifier/internal/store"
	"github.com/ZaphireLiu/ntfy-notifier/internal/subscribe"
)

// App wires config, storage, scripts, subscriptions and notifications together.
type App struct {
	cfgPath  string
	cfgDir   string
	iconPath string
	cfg      *config.Config
	log      *slog.Logger

	store     *store.Store
	pending   *activate.Pending
	exec      *action.Executor
	notifier  notify.Notifier
	engine    *luaengine.Engine
	router    *router.Router
	subs      []*subscribe.Subscriber
	limiter   *router.Limiter
	quiet     *config.QuietHours
	limits    map[string]int
	titles    map[string]string
	scriptDir string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	statusMu  sync.Mutex
	status    subscribe.Status
	statusErr error
	lastMsg   map[string]time.Time

	pausedUntil atomic.Int64

	stats    Stats
	recentMu sync.Mutex
	recent   []string
}

// Stats are monotonic counters exposed in the tray tooltip.
type Stats struct {
	Received   atomic.Int64
	Notified   atomic.Int64
	Suppressed atomic.Int64
	Dropped    atomic.Int64
	Paused     atomic.Int64
	Quiet      atomic.Int64
	Stale      atomic.Int64
	Errors     atomic.Int64
}

func New(cfgPath string, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	cfgDir := filepath.Dir(cfgPath)
	scriptDir := cfg.ResolveScriptsDir(cfgPath)
	if scriptDir != "" {
		if err := os.MkdirAll(scriptDir, 0o755); err != nil {
			return nil, fmt.Errorf("scripts dir: %w", err)
		}
	}
	iconPath := cfg.App.Icon
	if iconPath != "" && !filepath.IsAbs(iconPath) {
		iconPath = filepath.Join(cfgDir, iconPath)
	}
	if cfg.Server.TLSCA != "" && !filepath.IsAbs(cfg.Server.TLSCA) {
		cfg.Server.TLSCA = filepath.Join(cfgDir, cfg.Server.TLSCA)
	}

	st, err := store.Open(paths.StateFile())
	if err != nil {
		log.Warn("state load failed, starting empty", "err", err)
	}
	pending, err := activate.NewPending(paths.PendingDir(), cfg.Actions.PendingTTL.Std(), log)
	if err != nil {
		return nil, fmt.Errorf("pending dir: %w", err)
	}
	ex := action.New(cfg.Actions, log)
	ntf, err := notify.New(cfg.App.AppID, cfg.App.Name, log)
	if err != nil {
		return nil, fmt.Errorf("notifier: %w", err)
	}
	engine := luaengine.New(cfg.EnabledTopics(), scriptDir, cfg.Scripts.Timeout.Std(), st, log)
	quiet, err := config.ParseQuietHours(cfg.Notify.QuietHours)
	if err != nil {
		return nil, err
	}

	limits := make(map[string]int, len(cfg.Topics))
	titles := make(map[string]string, len(cfg.Topics))
	for _, t := range cfg.Topics {
		if t.MaxPerMin != nil {
			limits[t.Name] = *t.MaxPerMin
		} else {
			limits[t.Name] = cfg.Notify.MaxPerMin
		}
		titles[t.Name] = t.Title
	}

	a := &App{
		cfgPath:   cfgPath,
		cfgDir:    cfgDir,
		iconPath:  iconPath,
		cfg:       cfg,
		log:       log,
		store:     st,
		pending:   pending,
		exec:      ex,
		notifier:  ntf,
		engine:    engine,
		limiter:   router.NewLimiter(cfg.Notify.MaxPerMin),
		quiet:     quiet,
		limits:    limits,
		titles:    titles,
		scriptDir: scriptDir,
		ctx:       context.Background(),
		lastMsg:   map[string]time.Time{},
	}
	return a, nil
}

// Start begins subscriptions and script watching.
func (a *App) Start(parent context.Context) error {
	a.ctx, a.cancel = context.WithCancel(parent)

	topics := make([]string, 0, len(a.cfg.Topics))
	for _, t := range a.cfg.EnabledTopics() {
		topics = append(topics, t.Name)
	}
	dedupe := router.NewDeduper(1000, 10*time.Minute)
	if a.store != nil {
		// A restart replays the last cached message because the cursor only
		// moves back a few seconds. Remember that message so the replay is
		// discarded instead of producing a duplicate toast.
		for _, t := range topics {
			if id := a.store.LastID(t); id != "" {
				dedupe.Seed(t + "/" + id)
			}
		}
	}
	a.router = router.New(a.ctx, topics, a.process, dedupe, 100)
	a.router.Start()

	if a.scriptDir != "" {
		if err := a.engine.StartWatcher(a.ctx, a.scriptDir); err != nil {
			a.log.Warn("script watcher disabled", "err", err)
		}
	}

	sub, err := subscribe.New(subscribe.Options{
		Server:    a.cfg.Server,
		Topics:    topics,
		State:     a.store,
		Logger:    a.log,
		OnMessage: a.router.Dispatch,
		OnStatus:  a.onStatus,
		MaxAge:    a.cfg.Notify.MaxMessageAge.Std(),
		OnStale: func(m model.Message, age time.Duration) {
			a.stats.Stale.Add(1)
			a.log.Debug("stale message dropped",
				"topic", m.Topic, "id", m.ID, "age", age.Truncate(time.Second))
		},
	})
	if err != nil {
		a.router.Close()
		return err
	}
	a.subs = append(a.subs, sub)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		sub.Run(a.ctx)
	}()
	a.log.Info("started", "topics", topics, "mode", a.cfg.Server.Mode)
	return nil
}

func (a *App) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.router != nil {
		a.router.Close()
	}
	a.engine.Close()
	if a.store != nil {
		_ = a.store.Close()
	}
	if a.notifier != nil {
		_ = a.notifier.Close()
	}
}

// HandleMessage runs a message through the pipeline synchronously. It is used
// by --test-message and by tests.
func (a *App) HandleMessage(m model.Message) {
	a.process(m)
}

func (a *App) onStatus(st subscribe.Status, err error) {
	a.statusMu.Lock()
	prev := a.status
	a.status = st
	a.statusErr = err
	a.statusMu.Unlock()

	if st != prev {
		switch st {
		case subscribe.StatusConnected:
			a.log.Info("subscribed")
			a.addRecent("连接成功")
		case subscribe.StatusUnauthorized:
			a.log.Error("ntfy authentication failed", "err", err)
			a.addRecent("认证失败")
		case subscribe.StatusError:
			a.log.Warn("subscription error", "err", err)
		}
	}
}

func (a *App) process(m model.Message) {
	a.stats.Received.Add(1)
	now := time.Now()
	a.statusMu.Lock()
	a.lastMsg[m.Topic] = now
	a.statusMu.Unlock()

	if a.Paused() {
		a.stats.Paused.Add(1)
		return
	}

	spec, handled, err := a.engine.Handle(m.Topic, m)
	if err != nil {
		a.stats.Errors.Add(1)
		a.log.Warn("script error, falling back to default mapping", "topic", m.Topic, "id", m.ID, "err", err)
		a.addRecent(m.Topic + ": 脚本错误")
	}
	if handled && spec == nil {
		a.stats.Suppressed.Add(1)
		return
	}
	if !handled {
		spec = a.defaultSpec(m)
	}
	a.normalizeSpec(spec, m)
	n := a.buildNotification(spec)
	if n == nil {
		return
	}

	if a.quiet != nil && a.quiet.Contains(now) {
		if strings.EqualFold(a.cfg.Notify.QuietAction, "drop") {
			a.stats.Quiet.Add(1)
			return
		}
		n.Sound = "silent"
	}

	perMin, ok := a.limits[m.Topic]
	if !ok {
		perMin = a.cfg.Notify.MaxPerMin
	}
	if !a.limiter.Allow(m.Topic, perMin, now) {
		a.stats.Dropped.Add(1)
		a.log.Debug("rate limited", "topic", m.Topic)
		return
	}
	if err := a.notifier.Notify(a.ctx, *n); err != nil {
		a.stats.Errors.Add(1)
		return
	}
	a.stats.Notified.Add(1)
	a.addRecent(fmt.Sprintf("%s: %s", m.Topic, n.Title))
}

func (a *App) defaultSpec(m model.Message) *model.Spec {
	title := m.Title
	if strings.TrimSpace(title) == "" {
		title = a.titles[m.Topic]
	}
	if strings.TrimSpace(title) == "" {
		title = "[" + m.Topic + "]"
	}
	spec := &model.Spec{
		Title:    title,
		Body:     m.Message,
		Priority: m.Priority,
		Sound:    soundForPriority(m.Priority),
		Duration: durationForPriority(m.Priority),
		Click:    m.Click,
	}
	for _, ra := range m.Actions {
		if !strings.EqualFold(ra.Action, "view") || ra.URL == "" {
			continue
		}
		label := ra.Label
		if label == "" {
			label = "打开"
		}
		spec.Buttons = append(spec.Buttons, model.Button{
			Label:  label,
			Action: model.Action{Type: model.ActionOpen, URL: ra.URL},
		})
		if len(spec.Buttons) >= 3 {
			break
		}
	}
	return spec
}

func (a *App) normalizeSpec(spec *model.Spec, m model.Message) {
	if spec.Priority <= 0 {
		spec.Priority = m.Priority
	}
	if spec.Priority <= 0 {
		spec.Priority = 3
	}
	if spec.Title == "" {
		spec.Title = m.Title
	}
	if spec.Title == "" {
		spec.Title = a.titles[m.Topic]
	}
	if spec.Title == "" {
		spec.Title = "[" + m.Topic + "]"
	}
	if spec.Body == "" {
		spec.Body = m.Message
	}
	if spec.Sound == "" {
		spec.Sound = soundForPriority(spec.Priority)
	}
	switch spec.Sound {
	case "default", "mail", "silent":
	default:
		spec.Sound = "default"
	}
	if spec.Duration == "" {
		spec.Duration = durationForPriority(spec.Priority)
	}
	if spec.Duration != "long" {
		spec.Duration = "short"
	}
	if spec.Click != "" && a.exec.Validate(model.Action{Type: model.ActionOpen, URL: spec.Click}) != nil {
		spec.Click = ""
	}
	spec.Title = strings.ReplaceAll(spec.Title, "]]>", "]] >")
	spec.Body = strings.ReplaceAll(spec.Body, "]]>", "]] >")
	spec.Title = truncateRunes(spec.Title, 120)
	spec.Body = truncateRunes(spec.Body, a.cfg.Notify.LongBodyLimit)
}

func (a *App) buildNotification(spec *model.Spec) *model.Notification {
	icon := a.pickIcon(spec.Icon)
	n := &model.Notification{
		Title:    spec.Title,
		Body:     spec.Body,
		Sound:    spec.Sound,
		Duration: spec.Duration,
		Icon:     icon,
	}

	onClickLaunched := false
	if spec.OnClick != nil {
		if uri, ok := a.materialize(*spec.OnClick); ok {
			n.Launch = uri
			onClickLaunched = true
		}
	}
	if n.Launch == "" && spec.Click != "" {
		n.Launch = spec.Click
	}

	for _, b := range spec.Buttons {
		if len(n.Actions) >= 5 {
			break
		}
		uri, ok := a.materialize(b.Action)
		if !ok {
			continue
		}
		n.Actions = append(n.Actions, model.ToastAction{Label: b.Label, Launch: uri})
	}
	if onClickLaunched && spec.Click != "" && len(n.Actions) < 5 {
		n.Actions = append(n.Actions, model.ToastAction{Label: "打开链接", Launch: spec.Click})
	}
	if n.Title == "" && n.Body == "" {
		return nil
	}
	return n
}

// materialize turns an action into a launch URI. Open actions point directly at
// the URL; everything else goes through the pending store.
func (a *App) materialize(act model.Action) (string, bool) {
	if err := a.exec.Validate(act); err != nil {
		a.log.Warn("action rejected", "type", act.Type, "err", err)
		return "", false
	}
	if act.Type == model.ActionOpen {
		return act.URL, true
	}
	id, err := a.pending.Put(act)
	if err != nil {
		a.log.Warn("pending action write failed", "err", err)
		return "", false
	}
	return activate.URI(a.cfg.App.Scheme, id), true
}

func (a *App) pickIcon(specIcon string) string {
	if specIcon != "" {
		if filepath.IsAbs(specIcon) {
			if _, err := os.Stat(specIcon); err == nil {
				return specIcon
			}
		}
		a.log.Warn("script icon is not an absolute existing path", "icon", specIcon)
	}
	if a.iconPath != "" {
		if _, err := os.Stat(a.iconPath); err == nil {
			return a.iconPath
		}
	}
	return ""
}

func (a *App) Pause(d time.Duration) {
	a.pausedUntil.Store(time.Now().Add(d).Unix())
	a.addRecent("暂停通知")
}

func (a *App) Resume() {
	a.pausedUntil.Store(0)
	a.addRecent("恢复通知")
}

func (a *App) Paused() bool {
	until := a.pausedUntil.Load()
	return until > time.Now().Unix()
}

func (a *App) ReloadScripts() {
	a.engine.ReloadAll()
	a.addRecent("已重载脚本")
}

func (a *App) StatusText() string {
	if a.Paused() {
		return "已暂停"
	}
	if len(a.engine.Errors()) > 0 {
		return "脚本错误"
	}
	a.statusMu.Lock()
	st := a.status
	a.statusMu.Unlock()
	switch st {
	case subscribe.StatusConnected:
		return "已连接"
	case subscribe.StatusConnecting:
		return "连接中…"
	case subscribe.StatusUnauthorized:
		return "认证失败"
	case subscribe.StatusError:
		return "重连中…"
	default:
		return "已停止"
	}
}

func (a *App) StatusDetail() string {
	a.statusMu.Lock()
	st := a.status
	serr := a.statusErr
	last := make(map[string]time.Time, len(a.lastMsg))
	for k, v := range a.lastMsg {
		last[k] = v
	}
	a.statusMu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "状态: %s", st)
	if serr != nil {
		fmt.Fprintf(&b, " (%v)", serr)
	}
	fmt.Fprintf(&b, "\n收到 %d / 通知 %d / 抑制 %d / 丢弃 %d / 过期 %d",
		a.stats.Received.Load(), a.stats.Notified.Load(), a.stats.Suppressed.Load(),
		a.stats.Dropped.Load(), a.stats.Stale.Load())

	keys := make([]string, 0, len(last))
	for k := range last {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n%s: %s 前", k, time.Since(last[k]).Truncate(time.Second))
	}
	errs := a.engine.Errors()
	if len(errs) > 0 {
		errKeys := make([]string, 0, len(errs))
		for k := range errs {
			errKeys = append(errKeys, k)
		}
		sort.Strings(errKeys)
		for _, k := range errKeys {
			fmt.Fprintf(&b, "\n%s: %s", k, errs[k])
		}
	}
	return b.String()
}

func (a *App) Recent() []string {
	a.recentMu.Lock()
	defer a.recentMu.Unlock()
	out := make([]string, len(a.recent))
	copy(out, a.recent)
	return out
}

func (a *App) addRecent(s string) {
	a.recentMu.Lock()
	defer a.recentMu.Unlock()
	a.recent = append(a.recent, s)
	if len(a.recent) > 8 {
		a.recent = a.recent[len(a.recent)-8:]
	}
}

func (a *App) Scheme() string         { return a.cfg.App.Scheme }
func (a *App) ConfigPath() string     { return a.cfgPath }
func (a *App) ConfigDir() string      { return a.cfgDir }
func (a *App) ScriptDir() string      { return a.scriptDir }
func (a *App) LogDir() string         { return filepath.Dir(paths.LogFile()) }
func (a *App) AutostartEnabled() bool { return a.cfg.App.Autostart }

// TestToast sends a toast whose body click copies a short test string.
func (a *App) TestToast() error {
	act := model.Action{Type: model.ActionCopy, Text: "ntfy-notifier test", Sensitive: true}
	id, err := a.pending.Put(act)
	if err != nil {
		return err
	}
	n := model.Notification{
		Title:    a.cfg.App.Name,
		Body:     "测试通知：点击复制文本",
		Sound:    "default",
		Duration: "short",
		Icon:     a.pickIcon(""),
		Launch:   activate.URI(a.cfg.App.Scheme, id),
	}
	return a.notifier.Notify(a.ctx, n)
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + "…"
}

func soundForPriority(p int) string {
	switch {
	case p <= 2:
		return "silent"
	case p == 4:
		return "mail"
	default:
		return "default"
	}
}

func durationForPriority(p int) string {
	if p >= 5 {
		return "long"
	}
	return "short"
}
