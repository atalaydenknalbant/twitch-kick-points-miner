package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

//go:embed assets/miner2.png
var startupMinerPNG []byte

type startupTerminal struct {
	columns, rows         int
	cellWidth, cellHeight int
	interactive           bool
}

func startupTerminalSize(output io.Writer) startupTerminal {
	terminal := startupTerminal{columns: 80, rows: 32, cellWidth: 8, cellHeight: 16}
	if file, ok := output.(interface{ Fd() uintptr }); ok {
		if detected, ok := startupConsoleSize(file.Fd()); ok {
			terminal = detected
		}
	}
	for key, value := range map[string]*int{"COLUMNS": &terminal.columns, "LINES": &terminal.rows} {
		if size, err := strconv.Atoi(os.Getenv(key)); err == nil && size > 0 && size <= 1000 {
			*value = size
		}
	}
	return terminal
}

func startupBannerMode(terminal startupTerminal) string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("TKPM_BANNER_MODE")))
	if mode == "off" {
		return mode
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		if terminal.interactive || mode == "ascii" {
			return "ascii"
		}
		return "off"
	}
	switch mode {
	case "sixel", "blocks", "ascii":
		return mode
	}
	if !terminal.interactive {
		return "off"
	}
	if strings.EqualFold(os.Getenv("TERM_PROGRAM"), "vscode") || os.Getenv("TMUX") != "" || os.Getenv("STY") != "" {
		return "blocks"
	}
	if os.Getenv("WT_SESSION") != "" || strings.Contains(strings.ToLower(os.Getenv("TERM")), "sixel") {
		return "sixel"
	}
	return "blocks"
}

func writeStartupArt(output io.Writer) {
	if output == nil {
		return
	}
	terminal := startupTerminalSize(output)
	mode := startupBannerMode(terminal)
	var banner strings.Builder
	if mode != "off" && terminal.columns >= 8 && terminal.rows >= 10 {
		if img, err := png.Decode(bytes.NewReader(startupMinerPNG)); err == nil {
			width, height := startupImageSize(img.Bounds(), terminal, mode)
			raster := startupResize(img, width, height)
			banner.WriteByte('\n')
			switch mode {
			case "sixel":
				// Reserve rows and restore the cursor independently of sixel cursor behavior.
				rows := (height + terminal.cellHeight - 1) / terminal.cellHeight
				banner.WriteString(strings.Repeat("\n", rows))
				fmt.Fprintf(&banner, "\x1b[%dA\r\x1b7", rows)
				banner.WriteString(startupSixel(raster))
				fmt.Fprintf(&banner, "\x1b8\x1b[%dB\r\n", rows)
			case "blocks":
				banner.WriteString(startupBlocks(raster))
			case "ascii":
				banner.WriteString(startupASCII(raster))
			}
		}
	}
	if mode == "sixel" || mode == "blocks" {
		fmt.Fprintf(&banner, "%s%s%s\n\n", constants.ColorGreen, constants.ProductName, constants.ColorReset)
	} else {
		fmt.Fprintf(&banner, "%s\n\n", constants.ProductName)
	}
	_, _ = io.WriteString(output, banner.String())
}

func startupImageSize(bounds image.Rectangle, terminal startupTerminal, mode string) (int, int) {
	maxWidth := min(terminal.columns-2, 144) * terminal.cellWidth
	maxHeight := min(terminal.rows-8, 36) * terminal.cellHeight
	scale := math.Min(float64(maxWidth)/float64(bounds.Dx()), float64(maxHeight)/float64(bounds.Dy()))
	scale = math.Min(scale, 1)
	width, height := float64(bounds.Dx())*scale, float64(bounds.Dy())*scale
	if mode != "sixel" {
		width /= float64(terminal.cellWidth)
		height /= float64(terminal.cellHeight)
		if mode == "blocks" {
			height *= 2
		}
	}
	return max(1, int(math.Floor(width))), max(1, int(math.Floor(height)))
}

// Average source pixels with premultiplied alpha to retain edges and transparent space.
func startupResize(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0 := bounds.Min.Y + y*bounds.Dy()/height
		y1 := max(y0+1, bounds.Min.Y+(y+1)*bounds.Dy()/height)
		for x := 0; x < width; x++ {
			x0 := bounds.Min.X + x*bounds.Dx()/width
			x1 := max(x0+1, bounds.Min.X+(x+1)*bounds.Dx()/width)
			var red, green, blue, alpha uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
				}
			}
			samples := uint64((x1 - x0) * (y1 - y0))
			result.SetRGBA(x, y, color.RGBA{uint8(red / samples >> 8), uint8(green / samples >> 8), uint8(blue / samples >> 8), uint8(alpha / samples >> 8)})
		}
	}
	return result
}

func startupBlocks(img *image.RGBA) string {
	var out strings.Builder
	for y := 0; y < img.Bounds().Dy(); y += 2 {
		for x := 0; x < img.Bounds().Dx(); x++ {
			top, bottom := img.RGBAAt(x, y), img.RGBAAt(x, y+1)
			if top.A < 16 && bottom.A < 16 {
				out.WriteString("\x1b[0m ")
			} else if top.A < 16 {
				fmt.Fprintf(&out, "\x1b[49m\x1b[38;2;%d;%d;%dm\u2584", bottom.R, bottom.G, bottom.B)
			} else if bottom.A < 16 {
				fmt.Fprintf(&out, "\x1b[49m\x1b[38;2;%d;%d;%dm\u2580", top.R, top.G, top.B)
			} else {
				fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm\u2580", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
			}
		}
		out.WriteString("\x1b[0m\n")
	}
	return out.String()
}

func startupASCII(img *image.RGBA) string {
	const shades = " .,:;irsXA253hMHGS#9B&@"
	var out strings.Builder
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			pixel := img.RGBAAt(x, y)
			brightness := (299*int(pixel.R) + 587*int(pixel.G) + 114*int(pixel.B)) / 1000
			out.WriteByte(shades[brightness*(len(shades)-1)/255])
		}
		out.WriteByte('\n')
	}
	return out.String()
}
