package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

func TestWriteStartupArt(t *testing.T) {
	t.Setenv("TKPM_BANNER_MODE", "blocks")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("COLUMNS", "80")
	t.Setenv("LINES", "32")
	var output bytes.Buffer
	writeStartupArt(&output)
	text := output.String()
	if !strings.Contains(text, "\u2580") || !strings.Contains(text, "\x1b[38;2;") {
		t.Fatal("banner did not render image pixels in color")
	}
	if !strings.Contains(text, constants.ProductName) {
		t.Fatal("banner is missing the product name")
	}
	plain := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(text, "")
	for _, line := range strings.Split(plain, "\n") {
		if utf8.RuneCountInString(line) > 78 {
			t.Fatalf("banner wraps an 80 column terminal: %d", utf8.RuneCountInString(line))
		}
	}
	writeStartupArt(nil)
}

func TestStartupOutputMode(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	tests := []struct {
		name, mode, term, windows, noColor, want string
		interactive                              bool
	}{
		{name: "Windows Terminal", windows: "session", interactive: true, want: "sixel"},
		{name: "sixel terminal", term: "xterm-sixel", interactive: true, want: "sixel"},
		{name: "standard terminal", interactive: true, want: "blocks"},
		{name: "redirected Windows Terminal", windows: "session", want: "off"},
		{name: "explicit blocks", mode: "blocks", windows: "session", want: "blocks"},
		{name: "explicit ASCII", mode: "ascii", want: "ascii"},
		{name: "no color", noColor: "1", interactive: true, want: "ascii"},
		{name: "dumb terminal", term: "dumb", interactive: true, want: "ascii"},
		{name: "disabled", mode: "off", interactive: true, want: "off"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TKPM_BANNER_MODE", test.mode)
			t.Setenv("TERM", test.term)
			t.Setenv("WT_SESSION", test.windows)
			t.Setenv("NO_COLOR", test.noColor)
			if got := startupBannerMode(startupTerminal{interactive: test.interactive}); got != test.want {
				t.Fatalf("mode got %s, want %s", got, test.want)
			}
		})
	}
}

func TestStartupEmbeddedTerminalDoesNotInheritSixel(t *testing.T) {
	t.Setenv("TKPM_BANNER_MODE", "")
	t.Setenv("TERM", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("WT_SESSION", "parent-terminal")
	t.Setenv("TERM_PROGRAM", "vscode")
	if got := startupBannerMode(startupTerminal{interactive: true}); got != "blocks" {
		t.Fatalf("embedded terminal selected unsupported graphics: %s", got)
	}
}

func TestRedirectedStartupOutputIsPlain(t *testing.T) {
	t.Setenv("TKPM_BANNER_MODE", "")
	t.Setenv("WT_SESSION", "inherited-session")
	var output bytes.Buffer
	writeStartupArt(&output)
	if got := output.String(); got != constants.ProductName+"\n\n" {
		t.Fatalf("redirected output contains graphics: %q", got)
	}
}

func TestStartupImageSizePreservesAspectAndFits(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(startupMinerPNG))
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []startupTerminal{
		{columns: 40, rows: 18, cellWidth: 8, cellHeight: 16},
		{columns: 80, rows: 32, cellWidth: 8, cellHeight: 16},
		{columns: 140, rows: 48, cellWidth: 10, cellHeight: 22},
	} {
		for _, mode := range []string{"sixel", "blocks", "ascii"} {
			width, height := startupImageSize(img.Bounds(), terminal, mode)
			displayWidth, displayHeight := width, height
			tolerance := 1.0
			if mode != "sixel" {
				displayWidth *= terminal.cellWidth
				displayHeight *= terminal.cellHeight
				tolerance = float64(terminal.cellHeight)
				if mode == "blocks" {
					displayHeight /= 2
				}
			}
			if displayWidth > (terminal.columns-2)*terminal.cellWidth || displayHeight > (terminal.rows-8)*terminal.cellHeight {
				t.Fatalf("%s banner does not fit %+v: %dx%d", mode, terminal, displayWidth, displayHeight)
			}
			wantHeight := float64(displayWidth) * float64(img.Bounds().Dy()) / float64(img.Bounds().Dx())
			if math.Abs(float64(displayHeight)-wantHeight) > tolerance {
				t.Fatalf("%s banner is distorted: %dx%d", mode, displayWidth, displayHeight)
			}
		}
	}
}

func TestStartupResizePreservesAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(10, 20, 12, 22))
	img.SetNRGBA(10, 20, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(11, 20, color.NRGBA{B: 255, A: 0})
	resized := startupResize(img, 1, 1)
	pixel := resized.RGBAAt(0, 0)
	if pixel.R != 63 || pixel.G != 0 || pixel.B != 0 || pixel.A != 63 {
		t.Fatalf("transparent pixel contaminated the image: %+v", pixel)
	}
}

func TestStartupBlocksPreservePixelColors(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	img.SetRGBA(0, 1, color.RGBA{G: 255, A: 255})
	img.SetRGBA(1, 1, color.RGBA{B: 255, A: 255})
	img.SetRGBA(0, 2, color.RGBA{R: 255, B: 255, A: 255})
	output := startupBlocks(img)
	for _, expected := range []string{
		"\x1b[38;2;255;0;0m\x1b[48;2;0;255;0m\u2580",
		"\x1b[49m\x1b[38;2;0;0;255m\u2584",
		"\x1b[49m\x1b[38;2;255;0;255m\u2580",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing pixel colors %q in %q", expected, output)
		}
	}
	if !strings.HasSuffix(output, "\x1b[0m\n") {
		t.Fatal("block banner does not reset colors before subsequent text")
	}
}

func TestStartupSixelTransparentBandsAndCompression(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 7))
	for x := 0; x < 8; x++ {
		img.SetRGBA(x, 0, color.RGBA{A: 255})
		img.SetRGBA(x, 1, color.RGBA{A: 255})
	}
	img.SetRGBA(0, 6, color.RGBA{A: 255})
	got := startupSixel(img)
	black := startupSixelPalette.Index(color.RGBA{A: 255})
	if !strings.HasPrefix(got, "\x1bP0;1;0q\"1;1;8;7") || !strings.HasSuffix(got, fmt.Sprintf("#%d!8B-#%d@\x1b\\", black, black)) {
		t.Fatal("sixel does not preserve transparent pixels, final band, or repeated columns")
	}
}

func TestStartupASCIIFollowsSourceLuminance(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 1))
	img.SetRGBA(1, 0, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	img.SetRGBA(2, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	got := startupASCII(img)
	if got[0] != ' ' || got[1] == ' ' || got[2] != '@' || len(got) != 4 {
		t.Fatalf("ASCII does not follow source brightness: %q", got)
	}
}
