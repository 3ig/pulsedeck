package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// trayIcon draws the Pulsedeck mark (a three-quarter gauge ring with a centre
// dot) so no image files need to ship with the binary. template=true gives the
// black-with-alpha form macOS recolours for light and dark menu bars. On
// Windows the PNG is wrapped in an ICO container, which the tray requires.
func trayIcon(template bool) []byte {
	const size, ss = 64, 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	inside := func(x, y float64) bool {
		dx, dy := x-size/2, y-size/2
		d := math.Hypot(dx, dy)
		if d <= 6.5 {
			return true
		}
		if d < 17 || d > 26 {
			return false
		}
		// Open at the bottom: 270° sweep starting at 7:30.
		deg := math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
		return !(deg > 45+4 && deg < 135-4)
	}
	fg := color.NRGBA{0x8b, 0x6c, 0xff, 0xff}
	if template {
		fg = color.NRGBA{0, 0, 0, 0xff}
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					if inside(float64(x)+(float64(sx)+.5)/ss, float64(y)+(float64(sy)+.5)/ss) {
						hits++
					}
				}
			}
			c := fg
			c.A = uint8(hits * 255 / (ss * ss))
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	if runtime.GOOS != "windows" {
		return buf.Bytes()
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})               // reserved, type=icon, count
	ico.Write([]byte{size, size, 0, 0})                                      // width, height, palette, reserved
	binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})                 // planes, bits per pixel
	binary.Write(&ico, binary.LittleEndian, []uint32{uint32(buf.Len()), 22}) // data size, data offset
	ico.Write(buf.Bytes())
	return ico.Bytes()
}
