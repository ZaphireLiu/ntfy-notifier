//go:build windows

package singleinstance

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCreateMutexW = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateMutexW")

// Lock is a named mutex held for the lifetime of the process.
type Lock struct {
	handle windows.Handle
}

// Acquire creates (or opens) a named mutex. already=true means another
// instance owns it.
func Acquire(name string) (lock *Lock, already bool, err error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if h == 0 {
		return nil, false, callErr
	}
	handle := windows.Handle(h)
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(handle)
		return nil, true, nil
	}
	return &Lock{handle: handle}, false, nil
}

func (l *Lock) Release() {
	if l != nil && l.handle != 0 {
		_ = windows.CloseHandle(l.handle)
		l.handle = 0
	}
}
