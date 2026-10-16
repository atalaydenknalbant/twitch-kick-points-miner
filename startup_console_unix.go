//go:build linux || darwin

package main

import (
	"syscall"
	"unsafe"
)

func startupConsoleSize(fd uintptr) (startupTerminal, bool) {
	var size struct {
		Rows, Columns, PixelWidth, PixelHeight uint16
	}
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	if err != 0 || size.Columns == 0 || size.Rows == 0 {
		return startupTerminal{}, false
	}
	terminal := startupTerminal{
		columns: int(size.Columns), rows: int(size.Rows),
		cellWidth: 8, cellHeight: 16, interactive: true,
	}
	if size.PixelWidth >= size.Columns && size.PixelHeight >= size.Rows {
		terminal.cellWidth = int(size.PixelWidth / size.Columns)
		terminal.cellHeight = int(size.PixelHeight / size.Rows)
	}
	return terminal, true
}
