//go:build !windows && !linux && !darwin

package main

func startupConsoleSize(fd uintptr) (startupTerminal, bool) {
	return startupTerminal{}, false
}
