package main

import (
	"fmt"
	"image"
	"image/draw"
	"strings"
)

func startupSixel(img *image.RGBA) string {
	indexed := image.NewPaletted(img.Bounds(), startupSixelPalette)
	draw.FloydSteinberg.Draw(indexed, indexed.Bounds(), img, img.Bounds().Min)
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	var out strings.Builder
	out.WriteString("\x1bP0;1;0q")
	fmt.Fprintf(&out, "\"1;1;%d;%d", width, height)
	for index, entry := range indexed.Palette {
		r, g, b, _ := entry.RGBA()
		fmt.Fprintf(&out, "#%d;2;%d;%d;%d", index, (r*100+32767)/65535, (g*100+32767)/65535, (b*100+32767)/65535)
	}
	for bandY := 0; bandY < height; bandY += 6 {
		// Emit only colors present in this band and compress repeated sixel columns.
		planes := make([][]byte, len(indexed.Palette))
		for y := bandY; y < min(bandY+6, height); y++ {
			for x := 0; x < width; x++ {
				if img.RGBAAt(x, y).A < 16 {
					continue
				}
				index := indexed.ColorIndexAt(x, y)
				if planes[index] == nil {
					planes[index] = make([]byte, width)
				}
				planes[index][x] |= 1 << (y - bandY)
			}
		}
		first := true
		for index, plane := range planes {
			if plane == nil {
				continue
			}
			if !first {
				out.WriteByte('$')
			}
			first = false
			fmt.Fprintf(&out, "#%d", index)
			end := len(plane)
			for end > 0 && plane[end-1] == 0 {
				end--
			}
			for start := 0; start < end; {
				next := start + 1
				for next < end && plane[next] == plane[start] {
					next++
				}
				if next-start >= 4 {
					fmt.Fprintf(&out, "!%d%c", next-start, plane[start]+63)
				} else {
					for i := start; i < next; i++ {
						out.WriteByte(plane[start] + 63)
					}
				}
				start = next
			}
		}
		if bandY+6 < height {
			out.WriteByte('-')
		}
	}
	out.WriteString("\x1b\\")
	return out.String()
}
