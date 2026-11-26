//go:build windows

package action

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellExecuteW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func openURL(raw string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(target)),
		0, 0, 1, // SW_SHOWNORMAL
	)
	if r <= 32 {
		return errors.New("open: ShellExecute failed")
	}
	return nil
}

// OpenPath opens a local file or folder with the default handler.
func OpenPath(path string) error {
	return openURL(path)
}
