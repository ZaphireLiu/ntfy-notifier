//go:build windows

package autostart

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	valueName = "ntfy-notifier"
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// Set enables or disables starting the app at login for the current user.
func Set(enabled bool, exePath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enabled {
		return k.SetStringValue(valueName, fmt.Sprintf(`"%s" --autostart`, exePath))
	}
	err = k.DeleteValue(valueName)
	if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return err
	}
	return nil
}

// Enabled reports whether the Run value exists.
func Enabled(exePath string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.READ)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	return err == nil
}
