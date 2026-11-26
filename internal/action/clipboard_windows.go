//go:build windows

package action

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cfUnicodeText = 13

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procRegisterClipFmt  = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")

	clipOnce      sync.Once
	fmtNoHistory  uintptr
	fmtNoCloud    uintptr
	fmtNoMonitor  uintptr
	clipFormatsOK bool
)

const (
	gmemMoveable = 0x0002
)

func registerClipboardFormats() {
	clipOnce.Do(func() {
		name := func(s string) uintptr {
			p, err := windows.UTF16PtrFromString(s)
			if err != nil {
				return 0
			}
			r, _, _ := procRegisterClipFmt.Call(uintptr(unsafe.Pointer(p)))
			return r
		}
		fmtNoHistory = name("CanIncludeInClipboardHistory")
		fmtNoCloud = name("CanUploadToCloudClipboard")
		fmtNoMonitor = name("ExcludeClipboardContentFromMonitorProcessing")
		clipFormatsOK = fmtNoHistory != 0 && fmtNoCloud != 0 && fmtNoMonitor != 0
	})
}

func writeClipboard(text string, sensitive bool) error {
	registerClipboardFormats()

	var opened bool
	for i := 0; i < 5; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			opened = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("clipboard: open failed (another application may hold it)")
	}
	defer procCloseClipboard.Call()

	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("clipboard: empty failed")
	}

	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return fmt.Errorf("clipboard: %w", err)
	}
	ptr, err := allocGlobal(uintptr(len(utf16) * 2))
	if err != nil {
		return err
	}
	locked, _, _ := procGlobalLock.Call(ptr)
	if locked == 0 {
		procGlobalFree.Call(ptr)
		return fmt.Errorf("clipboard: GlobalLock failed")
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(locked)), len(utf16))
	copy(dst, utf16)
	procGlobalUnlock.Call(ptr)
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, ptr); r == 0 {
		procGlobalFree.Call(ptr)
		return fmt.Errorf("clipboard: set text failed")
	}

	if sensitive && clipFormatsOK {
		setDword(fmtNoHistory, 0)
		setDword(fmtNoCloud, 0)
		// Presence of this format alone excludes the content from monitoring.
		procSetClipboardData.Call(fmtNoMonitor, 0)
	}
	return nil
}

func allocGlobal(size uintptr) (uintptr, error) {
	ptr, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if ptr == 0 {
		return 0, fmt.Errorf("clipboard: GlobalAlloc failed: %v", err)
	}
	return ptr, nil
}

func setDword(format uintptr, value uint32) {
	ptr, err := allocGlobal(4)
	if err != nil {
		return
	}
	locked, _, _ := procGlobalLock.Call(ptr)
	if locked == 0 {
		procGlobalFree.Call(ptr)
		return
	}
	*(*uint32)(unsafe.Pointer(locked)) = value
	procGlobalUnlock.Call(ptr)
	if r, _, _ := procSetClipboardData.Call(format, ptr); r == 0 {
		procGlobalFree.Call(ptr)
	}
}
