package paths

import (
	"os"
	"path/filepath"
)

const appDirName = "ntfy-notifier"

// ExeDir returns the directory containing the running executable.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// DefaultConfigPath returns <exe dir>/config.toml.
func DefaultConfigPath() string {
	return filepath.Join(ExeDir(), "config.toml")
}

// DataDir is %APPDATA%\ntfy-notifier on Windows, ~/.config/ntfy-notifier on
// Linux (used for development).
func DataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = ExeDir()
	}
	return filepath.Join(base, appDirName)
}

// CacheDir is %LOCALAPPDATA%\ntfy-notifier on Windows, ~/.cache/ntfy-notifier
// on Linux.
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, appDirName)
}

func PendingDir() string { return filepath.Join(CacheDir(), "pending") }
func StateFile() string  { return filepath.Join(DataDir(), "state.json") }
func LogFile() string    { return filepath.Join(DataDir(), "logs", "app.log") }
