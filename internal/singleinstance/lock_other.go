//go:build !windows

package singleinstance

// Lock is a no-op on non-Windows development platforms.
type Lock struct{}

func Acquire(string) (*Lock, bool, error) { return &Lock{}, false, nil }
func (l *Lock) Release()                  {}
