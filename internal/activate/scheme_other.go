//go:build !windows

package activate

import "errors"

func RegisterScheme(string, string) error {
	return errors.New("URI scheme registration is only supported on Windows")
}

func UnregisterScheme(string) error {
	return errors.New("URI scheme registration is only supported on Windows")
}

func NotifyShell() {}

func OpenPath(string) error {
	return errors.New("open path is only supported on Windows")
}
