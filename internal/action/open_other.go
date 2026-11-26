//go:build !windows

package action

import (
	"os/exec"
)

func openURL(raw string) error {
	return exec.Command("xdg-open", raw).Start()
}

func OpenPath(path string) error {
	return exec.Command("xdg-open", path).Start()
}
