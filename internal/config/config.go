package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that unmarshals from TOML strings like "200ms".
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the root of config.toml.
type Config struct {
	App     App     `toml:"app"`
	Server  Server  `toml:"server"`
	Notify  Notify  `toml:"notify"`
	Actions Actions `toml:"actions"`
	Scripts Scripts `toml:"scripts"`
	Topics  []Topic `toml:"topic"`
}

type App struct {
	Name      string `toml:"name"`
	AppID     string `toml:"app_id"`
	Scheme    string `toml:"scheme"`
	Autostart bool   `toml:"autostart"`
	Icon      string `toml:"icon"`
}

type Server struct {
	BaseURL            string   `toml:"base_url"`
	Token              string   `toml:"token"`
	Username           string   `toml:"username"`
	Password           string   `toml:"password"`
	TLSCA              string   `toml:"tls_ca"`
	InsecureSkipVerify bool     `toml:"insecure_skip_verify"`
	Mode               string   `toml:"mode"` // stream | poll
	Proxy              string   `toml:"proxy"`
	InitialSince       string   `toml:"initial_since"`
	PollInterval       Duration `toml:"poll_interval"`
}

type Notify struct {
	MaxPerMin     int      `toml:"max_per_min"`
	QuietHours    string   `toml:"quiet_hours"`
	QuietAction   string   `toml:"quiet_action"` // silent | drop
	LongBodyLimit int      `toml:"long_body_limit"`
	MaxMessageAge Duration `toml:"max_message_age"` // 0 disables the check
}

type Actions struct {
	AllowOpen      []string `toml:"allow_open"`
	AllowHTTP      bool     `toml:"allow_http"`
	HTTPAllowHosts []string `toml:"http_allow_hosts"`
	AllowRun       bool     `toml:"allow_run"`
	PendingTTL     Duration `toml:"pending_ttl"`
}

type Scripts struct {
	Dir     string   `toml:"dir"`
	Timeout Duration `toml:"timeout"`
}

type Topic struct {
	Name      string `toml:"name"`
	MaxPerMin *int   `toml:"max_per_min"`
	Script    string `toml:"script"`
	Enabled   *bool  `toml:"enabled"`
	Title     string `toml:"title"`
}

func (t Topic) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// ScriptPath returns the Lua script path for this topic. An explicit script
// setting wins; otherwise scripts/<topic>.lua is used.
func (t Topic) ScriptPath(scriptsDir string) string {
	if t.Script != "" {
		return t.Script
	}
	return filepath.Join(scriptsDir, t.Name+".lua")
}

var (
	topicRe  = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)
	schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)
)

// Default returns a Config pre-filled with defaults. Load decodes on top of it
// so omitted fields keep sensible values.
func Default() *Config {
	return &Config{
		App: App{
			Name:   "ntfy Notifier",
			AppID:  "com.example.ntfynotifier",
			Scheme: "ntfy-notifier",
		},
		Server: Server{
			Mode:         "stream",
			PollInterval: Duration(30 * time.Second),
		},
		Notify: Notify{
			MaxPerMin:     10,
			QuietAction:   "silent",
			LongBodyLimit: 300,
			MaxMessageAge: Duration(5 * time.Minute),
		},
		Actions: Actions{
			AllowOpen:  []string{"http", "https"},
			AllowHTTP:  true,
			PendingTTL: Duration(10 * time.Minute),
		},
		Scripts: Scripts{
			Dir:     "scripts",
			Timeout: Duration(200 * time.Millisecond),
		},
	}
}

// Load reads and validates a TOML config file.
func Load(path string) (*Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) normalize() {
	c.Server.BaseURL = strings.TrimRight(strings.TrimSpace(c.Server.BaseURL), "/")
	c.Server.Mode = strings.ToLower(strings.TrimSpace(c.Server.Mode))
	c.App.Scheme = strings.ToLower(strings.TrimSpace(c.App.Scheme))
	c.Notify.QuietAction = strings.ToLower(strings.TrimSpace(c.Notify.QuietAction))
	for i := range c.Actions.AllowOpen {
		c.Actions.AllowOpen[i] = strings.ToLower(strings.TrimSpace(c.Actions.AllowOpen[i]))
	}
	for i := range c.Actions.HTTPAllowHosts {
		c.Actions.HTTPAllowHosts[i] = strings.ToLower(strings.TrimSpace(c.Actions.HTTPAllowHosts[i]))
	}
	if c.Server.PollInterval <= 0 {
		c.Server.PollInterval = Duration(30 * time.Second)
	}
}

// Validate checks the config for mistakes that would otherwise surface at
// runtime.
func (c *Config) Validate() error {
	var errs []error

	if strings.TrimSpace(c.App.Name) == "" {
		errs = append(errs, errors.New("app.name must not be empty"))
	}
	if strings.TrimSpace(c.App.AppID) == "" {
		errs = append(errs, errors.New("app.app_id must not be empty"))
	}
	if !schemeRe.MatchString(c.App.Scheme) {
		errs = append(errs, fmt.Errorf("app.scheme %q is not a valid URI scheme", c.App.Scheme))
	}

	if c.Server.BaseURL == "" {
		errs = append(errs, errors.New("server.base_url is required"))
	} else if u, err := url.Parse(c.Server.BaseURL); err != nil {
		errs = append(errs, fmt.Errorf("server.base_url: %w", err))
	} else if u.Scheme != "http" && u.Scheme != "https" {
		errs = append(errs, fmt.Errorf("server.base_url must be http(s), got %q", u.Scheme))
	} else if u.Host == "" {
		errs = append(errs, errors.New("server.base_url has no host"))
	}
	if c.Server.Mode != "stream" && c.Server.Mode != "poll" {
		errs = append(errs, fmt.Errorf("server.mode must be stream or poll, got %q", c.Server.Mode))
	}
	if c.Server.PollInterval <= 0 {
		errs = append(errs, errors.New("server.poll_interval must be positive"))
	}

	if c.Notify.MaxPerMin < 0 {
		errs = append(errs, errors.New("notify.max_per_min must be >= 0"))
	}
	if c.Notify.LongBodyLimit < 0 {
		errs = append(errs, errors.New("notify.long_body_limit must be >= 0"))
	}
	if c.Notify.MaxMessageAge < 0 {
		errs = append(errs, errors.New("notify.max_message_age must be >= 0"))
	}
	if c.Notify.QuietAction != "silent" && c.Notify.QuietAction != "drop" {
		errs = append(errs, fmt.Errorf("notify.quiet_action must be silent or drop, got %q", c.Notify.QuietAction))
	}
	if strings.TrimSpace(c.Notify.QuietHours) != "" {
		if _, err := ParseQuietHours(c.Notify.QuietHours); err != nil {
			errs = append(errs, fmt.Errorf("notify.quiet_hours: %w", err))
		}
	}

	if c.Actions.PendingTTL <= 0 {
		errs = append(errs, errors.New("actions.pending_ttl must be positive"))
	}
	if c.Scripts.Timeout <= 0 {
		errs = append(errs, errors.New("scripts.timeout must be positive"))
	}
	if strings.TrimSpace(c.Scripts.Dir) == "" {
		errs = append(errs, errors.New("scripts.dir must not be empty"))
	}

	if len(c.Topics) == 0 {
		errs = append(errs, errors.New("at least one [[topic]] is required"))
	}
	seen := make(map[string]string, len(c.Topics))
	enabled := 0
	for i, t := range c.Topics {
		if !topicRe.MatchString(t.Name) {
			errs = append(errs, fmt.Errorf("topic[%d] name %q must match [-_A-Za-z0-9]{1,64}", i, t.Name))
		}
		lower := strings.ToLower(t.Name)
		if prev, ok := seen[lower]; ok {
			errs = append(errs, fmt.Errorf("topic[%d] %q collides with %q on a case-insensitive filesystem", i, t.Name, prev))
		}
		seen[lower] = t.Name
		if t.MaxPerMin != nil && *t.MaxPerMin < 0 {
			errs = append(errs, fmt.Errorf("topic %q max_per_min must be >= 0", t.Name))
		}
		if t.IsEnabled() {
			enabled++
		}
	}
	if enabled == 0 {
		errs = append(errs, errors.New("at least one topic must be enabled"))
	}

	return errors.Join(errs...)
}

// EnabledTopics returns the topics that should be subscribed.
func (c *Config) EnabledTopics() []Topic {
	out := make([]Topic, 0, len(c.Topics))
	for _, t := range c.Topics {
		if t.IsEnabled() {
			out = append(out, t)
		}
	}
	return out
}

// ResolveScriptsDir resolves a relative scripts dir against the config dir.
func (c *Config) ResolveScriptsDir(configPath string) string {
	if strings.TrimSpace(c.Scripts.Dir) == "" {
		return ""
	}
	if filepath.IsAbs(c.Scripts.Dir) || filepath.VolumeName(c.Scripts.Dir) != "" {
		return c.Scripts.Dir
	}
	return filepath.Join(filepath.Dir(configPath), c.Scripts.Dir)
}
