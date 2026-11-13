// mkicons renders the Pulsedeck app icon (the same artwork as client/icon.svg)
// with a transparent background and writes every raster format the packages
// need. Run from the repository root:  go run ./packaging/mkicons
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// The design is drawn on a 108x108 canvas.
const canvas = 108.0

type rgba struct{ r, g, b, a float64 }

func over(top, bottom rgba) rgba {
	a := top.a + bottom.a*(1-top.a)
	if a == 0 {
		return rgba{}
	}
	mix := func(t, b float64) float64 { return (t*top.a + b*bottom.a*(1-top.a)) / a }
	return rgba{mix(top.r, bottom.r), mix(top.g, bottom.g), mix(top.b, bottom.b), a}
}

func hex(v uint32, a float64) rgba {
	return rgba{float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255, a}
}

// onArc reports whether (x, y) lies on a stroked arc with round caps.
func onArc(x, y, cx, cy, r, width, startDeg, sweepDeg float64) bool {
	dx, dy := x-cx, y-cy
	if math.Abs(math.Hypot(dx, dy)-r) <= width/2 {
		deg := math.Mod(math.Atan2(dy, dx)*180/math.Pi-startDeg+720, 360)
		if deg <= sweepDeg {
			return true
		}
	}
	for _, end := range []float64{startDeg, startDeg + sweepDeg} {
		ex, ey := cx+r*math.Cos(end*math.Pi/180), cy+r*math.Sin(end*math.Pi/180)
		if math.Hypot(x-ex, y-ey) <= width/2 {
			return true
		}
	}
	return false
}

func sample(x, y float64) rgba {
	// Rounded square background.
	const radius = 24.0
	qx, qy := math.Max(math.Abs(x-54)-(54-radius), 0), math.Max(math.Abs(y-54)-(54-radius), 0)
	if math.Hypot(qx, qy) > radius {
		return rgba{}
	}
	c := hex(0x0e1118, 1)
	t := (x + y) / (2 * canvas) // diagonal gradient
	accent := rgba{0x9b/255.0 + (0x4f-0x9b)/255.0*t, 0x7b/255.0 + (0x8c-0x7b)/255.0*t, 1, 1}
	if onArc(x, y, 54, 54, 27, 8, 135, 270) {
		c = over(hex(0xffffff, 0.12), c)
	}
	if onArc(x, y, 54, 54, 27, 8, 135, 172.8) || math.Hypot(x-54, y-54) <= 6 {
		c = over(accent, c)
	}
	return c
}

func render(size int) *image.NRGBA {
	const ss = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := canvas / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := sample((float64(px)+(float64(sx)+.5)/ss)*scale, (float64(py)+(float64(sy)+.5)/ss)*scale)
					r, g, b, a = r+c.r*c.a, g+c.g*c.a, b+c.b*c.a, a+c.a
				}
			}
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{uint8(r / a * 255), uint8(g / a * 255), uint8(b / a * 255), uint8(a / (ss * ss) * 255)})
			}
		}
	}
	return img
}

func writePNG(path string, size int) {
	var buf bytes.Buffer
	png.Encode(&buf, render(size))
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

// writeICO stores classic 32-bit bitmap entries, which every tool that reads
// .ico files (including installer builders) understands.
func writeICO(path string, sizes ...int) {
	var dir, body bytes.Buffer
	binary.Write(&dir, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for _, size := range sizes {
		img := render(size)
		var e bytes.Buffer
		binary.Write(&e, binary.LittleEndian, []int32{40, int32(size), int32(size * 2)})
		binary.Write(&e, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&e, binary.LittleEndian, []uint32{0, uint32(size * size * 4), 0, 0, 0, 0})
		for y := size - 1; y >= 0; y-- { // bottom-up BGRA
			for x := 0; x < size; x++ {
				c := img.NRGBAAt(x, y)
				e.Write([]byte{c.B, c.G, c.R, c.A})
			}
		}
		e.Write(make([]byte, (size+31)/32*4*size)) // AND mask, unused with alpha
		dir.Write([]byte{byte(size), byte(size), 0, 0})
		binary.Write(&dir, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&dir, binary.LittleEndian, []uint32{uint32(e.Len()), uint32(offset)})
		offset += e.Len()
		body.Write(e.Bytes())
	}
	if err := os.WriteFile(path, append(dir.Bytes(), body.Bytes()...), 0o644); err != nil {
		panic(err)
	}
}

func main() {
	writePNG("packaging/icon.png", 1024)
	writePNG("packaging/linux/pulsedeck.png", 256)
	writeICO("packaging/windows/pulsedeck.ico", 16, 24, 32, 48, 64)
}
