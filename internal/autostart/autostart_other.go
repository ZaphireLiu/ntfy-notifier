//go:build !windows

package autostart

func Set(bool, string) error { return nil }
func Enabled(string) bool    { return false }
