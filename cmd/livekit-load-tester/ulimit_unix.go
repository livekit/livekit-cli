//go:build linux || darwin
// +build linux darwin

package main

import (
	"syscall"
)

func raiseULimit() error {
	// raise ulimit
	var rLimit syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		return err
	}
	rLimit.Max = 65535
	rLimit.Cur = 65535
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
}
