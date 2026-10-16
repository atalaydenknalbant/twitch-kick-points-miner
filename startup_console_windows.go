//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func startupConsoleSize(fd uintptr) (startupTerminal, bool) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	var mode uint32
	if err := syscall.GetConsoleMode(syscall.Handle(fd), &mode); err != nil {
		return startupTerminal{}, false
	}
	_, _, _ = kernel.NewProc("SetConsoleMode").Call(fd, uintptr(mode|0x5))
	_, _, _ = kernel.NewProc("SetConsoleOutputCP").Call(65001)
	var info struct {
		Width, Height            int16
		CursorX, CursorY         int16
		Attributes               uint16
		Left, Top, Right, Bottom int16
		MaxWidth, MaxHeight      int16
	}
	result, _, _ := kernel.NewProc("GetConsoleScreenBufferInfo").Call(fd, uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return startupTerminal{}, false
	}
	terminal := startupTerminal{
		columns: int(info.Right-info.Left) + 1, rows: int(info.Bottom-info.Top) + 1,
		cellWidth: 8, cellHeight: 16, interactive: true,
	}
	var font struct {
		Size, Index    uint32
		Width, Height  int16
		Family, Weight uint32
		Face           [32]uint16
	}
	font.Size = uint32(unsafe.Sizeof(font))
	if result, _, _ := kernel.NewProc("GetCurrentConsoleFontEx").Call(fd, 0, uintptr(unsafe.Pointer(&font))); result != 0 && font.Width > 0 && font.Height > 0 {
		terminal.cellWidth, terminal.cellHeight = int(font.Width), int(font.Height)
	}
	return terminal, true
}
