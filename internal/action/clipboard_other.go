//go:build !windows

package action

import "errors"

func writeClipboard(string, bool) error {
	return errors.New("clipboard: only supported on Windows")
}
