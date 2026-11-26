package activate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ZaphireLiu/ntfy-notifier/internal/model"
)

var idRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Pending stores the payload for a toast click until it is used or expires.
type Pending struct {
	dir string
	ttl time.Duration
	log *slog.Logger
}

type pendingFile struct {
	V       int          `json:"v"`
	Created int64        `json:"created"`
	Expires int64        `json:"expires"`
	Action  model.Action `json:"action"`
}

func NewPending(dir string, ttl time.Duration, log *slog.Logger) (*Pending, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	p := &Pending{dir: dir, ttl: ttl, log: log}
	p.Cleanup()
	return p, nil
}

func (p *Pending) Dir() string { return p.dir }

// Put writes an action and returns its random id.
func (p *Pending) Put(a model.Action) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf[:])
	now := time.Now()
	data, err := json.Marshal(pendingFile{
		V:       1,
		Created: now.Unix(),
		Expires: now.Add(p.ttl).UnixMilli(),
		Action:  a,
	})
	if err != nil {
		return "", err
	}
	final := filepath.Join(p.dir, id+".json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return id, nil
}

// Take loads and deletes a pending action.
func (p *Pending) Take(id string) (*model.Action, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("invalid action id")
	}
	path := filepath.Join(p.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(path)

	var pf pendingFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("decode pending action: %w", err)
	}
	if pf.Expires > 0 && time.Now().UnixMilli() > pf.Expires {
		return nil, fmt.Errorf("action expired")
	}
	return &pf.Action, nil
}

// Cleanup removes stale files, including temp files from interrupted writes.
func (p *Pending) Cleanup() {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-p.ttl)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(p.dir, e.Name()))
		}
	}
}
