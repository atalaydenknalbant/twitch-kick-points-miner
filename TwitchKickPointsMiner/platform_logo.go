package twitchchannelpointsminer

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/atalaydenknalbant/twitch-kick-points-miner/TwitchKickPointsMiner/constants"
)

const terminalLogoSize = 14

//go:embed assets/twitch.png
var twitchLogoPNG []byte

//go:embed assets/kick.png
var kickLogoPNG []byte

var (
	terminalLogosOnce sync.Once
	twitchLogoSixel   string
	kickLogoSixel     string
)

type terminalPlatformWriter struct {
	output io.Writer
}

func (w terminalPlatformWriter) Write(p []byte) (int, error) {
	rendered := renderConsolePlatformTokens(string(p))
	if _, err := w.output.Write([]byte(rendered)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func renderConsolePlatformTokens(message string) string {
	if !strings.Contains(message, constants.PlatformTwitchToken) && !strings.Contains(message, constants.PlatformKickToken) {
		return message
	}

	twitch, kick := unicodePlatformLogos()
	switch terminalPlatformLogoMode() {
	case "sixel":
		terminalLogosOnce.Do(func() {
			twitchLogoSixel = buildTerminalLogoSixel(twitchLogoPNG, false)
			kickLogoSixel = buildTerminalLogoSixel(kickLogoPNG, true)
		})
		if twitchLogoSixel != "" && kickLogoSixel != "" {
			twitch = twitchLogoSixel + "\x1b[2C"
			kick = kickLogoSixel + "\x1b[2C"
		}
	case "text":
		twitch = "Twitch"
		kick = "Kick"
	}

	message = strings.ReplaceAll(message, constants.PlatformTwitchToken, twitch)
	return strings.ReplaceAll(message, constants.PlatformKickToken, kick)
}

func plainPlatformTokens(message string) string {
	message = strings.ReplaceAll(message, constants.PlatformTwitchToken, "Twitch")
	return strings.ReplaceAll(message, constants.PlatformKickToken, "Kick")
}

func unicodePlatformLogos() (string, string) {
	return "\033[1;38;5;141mT" + constants.ColorReset, constants.ColorGreen + "K" + constants.ColorReset
}

func terminalPlatformLogoMode() string {
	mode := strings.TrimSpace(os.Getenv("TKPM_PLATFORM_LOGOS"))
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv("SBPM_PLATFORM_LOGOS"))
	}
	switch strings.ToLower(mode) {
	case "sixel":
		return "sixel"
	case "text":
		return "text"
	case "unicode":
		return "unicode"
	}
	if strings.TrimSpace(os.Getenv("WT_SESSION")) != "" || strings.Contains(strings.ToLower(os.Getenv("TERM")), "sixel") {
		return "sixel"
	}
	return "unicode"
}

type terminalLogoColor struct {
	r uint8
	g uint8
	b uint8
}

func buildTerminalLogoSixel(data []byte, kick bool) string {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	pixels, palette := rasterizeTerminalLogo(img, kick)
	return encodeTerminalLogoSixel(pixels, terminalLogoSize, terminalLogoSize, palette)
}

func rasterizeTerminalLogo(img image.Image, kick bool) ([]int, []terminalLogoColor) {
	palette := []terminalLogoColor{{r: 100, g: 65, b: 165}}
	if kick {
		palette = []terminalLogoColor{
			{r: 27, g: 27, b: 27},
			{r: 83, g: 252, b: 24},
		}
	}

	bounds, ok := terminalLogoOpaqueBounds(img)
	if !ok {
		return make([]int, terminalLogoSize*terminalLogoSize), palette
	}
	pixels := make([]int, terminalLogoSize*terminalLogoSize)
	for i := range pixels {
		pixels[i] = -1
	}

	for y := 0; y < terminalLogoSize; y++ {
		for x := 0; x < terminalLogoSize; x++ {
			sx0 := bounds.Min.X + x*bounds.Dx()/terminalLogoSize
			sx1 := bounds.Min.X + (x+1)*bounds.Dx()/terminalLogoSize
			sy0 := bounds.Min.Y + y*bounds.Dy()/terminalLogoSize
			sy1 := bounds.Min.Y + (y+1)*bounds.Dy()/terminalLogoSize
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			if sy1 <= sy0 {
				sy1 = sy0 + 1
			}
			alpha, red, green, blue, samples := averageTerminalLogoPixel(img, sx0, sx1, sy0, sy1)
			if samples == 0 || alpha < 40 {
				continue
			}
			index := 0
			if kick && int(green) > int(red)*2 && int(green) > int(blue)*2 {
				index = 1
			}
			pixels[y*terminalLogoSize+x] = index
		}
	}
	return pixels, palette
}

func terminalLogoOpaqueBounds(img image.Image) (image.Rectangle, bool) {
	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel.A < 16 {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if !found {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

func averageTerminalLogoPixel(img image.Image, x0, x1, y0, y1 int) (uint8, uint8, uint8, uint8, int) {
	var alphaTotal, redTotal, greenTotal, blueTotal int
	samples := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			alphaTotal += int(pixel.A)
			redTotal += int(pixel.R) * int(pixel.A)
			greenTotal += int(pixel.G) * int(pixel.A)
			blueTotal += int(pixel.B) * int(pixel.A)
			samples++
		}
	}
	if samples == 0 || alphaTotal == 0 {
		return 0, 0, 0, 0, samples
	}
	return uint8(alphaTotal / samples), uint8(redTotal / alphaTotal), uint8(greenTotal / alphaTotal), uint8(blueTotal / alphaTotal), samples
}

func encodeTerminalLogoSixel(pixels []int, width, height int, palette []terminalLogoColor) string {
	if width <= 0 || height <= 0 || len(pixels) != width*height || len(palette) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\x1bP0;1;0q")
	fmt.Fprintf(&out, "\"1;1;%d;%d", width, height)
	for index, entry := range palette {
		fmt.Fprintf(&out, "#%d;2;%d;%d;%d", index, terminalColorPercent(entry.r), terminalColorPercent(entry.g), terminalColorPercent(entry.b))
	}
	for bandY := 0; bandY < height; bandY += 6 {
		for paletteIndex := range palette {
			fmt.Fprintf(&out, "#%d", paletteIndex)
			for x := 0; x < width; x++ {
				bits := 0
				for bit := 0; bit < 6; bit++ {
					y := bandY + bit
					if y < height && pixels[y*width+x] == paletteIndex {
						bits |= 1 << bit
					}
				}
				out.WriteByte(byte(63 + bits))
			}
			if paletteIndex < len(palette)-1 {
				out.WriteByte('$')
			}
		}
		if bandY+6 < height {
			out.WriteByte('-')
		}
	}
	out.WriteString("\x1b\\")
	return out.String()
}

func terminalColorPercent(value uint8) int {
	return (int(value)*100 + 127) / 255
}
