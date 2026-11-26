//go:build windows

package activate

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procSHChangeNotify = shell32.NewProc("SHChangeNotify")
	procShellExecuteW  = shell32.NewProc("ShellExecuteW")
)

// RegisterScheme registers the custom URI scheme under HKCU. It is idempotent
// and rewrites the command if the executable moved.
func RegisterScheme(scheme, exePath string) error {
	base := `Software\Classes\` + scheme
	k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", "URL:"+scheme); err != nil {
		return err
	}
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	icon, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\DefaultIcon`, registry.WRITE)
	if err != nil {
		return err
	}
	_ = icon.SetStringValue("", fmt.Sprintf("\"%s\",0", exePath))
	icon.Close()

	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.WRITE)
	if err != nil {
		return err
	}
	_ = cmd.SetStringValue("", fmt.Sprintf(`"%s" --activate "%%1"`, exePath))
	cmd.Close()

	NotifyShell()
	return nil
}

func UnregisterScheme(scheme string) error {
	if err := deleteKeyRecursive(registry.CURRENT_USER, `Software\Classes\`+scheme); err != nil {
		return err
	}
	NotifyShell()
	return nil
}

func deleteKeyRecursive(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.READ|registry.WRITE)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil
		}
		return err
	}
	names, _ := k.ReadSubKeyNames(-1)
	_ = k.Close()
	for _, name := range names {
		if err := deleteKeyRecursive(root, path+`\`+name); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(root, path); err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return err
	}
	return nil
}

// NotifyShell tells Explorer that file associations changed.
func NotifyShell() {
	const shcneAssocChanged = 0x08000000
	procSHChangeNotify.Call(shcneAssocChanged, 0, 0, 0)
}

// OpenPath opens a file or folder in Explorer.
func OpenPath(path string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(
		0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1,
	)
	if r <= 32 {
		return errors.New("open path failed")
	}
	return nil
}
