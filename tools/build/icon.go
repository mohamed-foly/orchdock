package main

import (
	"image"
	"image/color"
	"math"
)

// appIcon draws the placeholder application icon at n×n pixels: a rounded
// tile with three terminal lines. It is replaced once the final icon exists.
func appIcon(n int, _ bool) *image.NRGBA {
	tile := color.NRGBA{0x2B, 0x3A, 0x55, 0xFF}
	line := color.NRGBA{0xE8, 0xEE, 0xF5, 0xFF}
	accent := color.NRGBA{0x5B, 0xC0, 0x8A, 0xFF}
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	px := 100 / float64(n) // units per pixel on a 100-unit canvas
	bars := []struct {
		x0, x1, y float64
		c         color.NRGBA
	}{
		{24, 46, 32, accent},
		{24, 76, 50, line},
		{24, 62, 68, line},
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			p := [2]float64{(float64(x) + 0.5) * px, (float64(y) + 0.5) * px}
			c := over(color.NRGBA{}, tile, cover(roundBox(p, 50, 50, 50, 22), px))
			for _, b := range bars {
				c = over(c, b.c, cover(capsule(p, b.x0, b.x1, b.y, 6), px))
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// Signed distances: negative inside.
func roundBox(p [2]float64, cx, cy, half, r float64) float64 {
	qx := math.Abs(p[0]-cx) - (half - r)
	qy := math.Abs(p[1]-cy) - (half - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func capsule(p [2]float64, x0, x1, y, r float64) float64 {
	dx := p[0] - math.Min(math.Max(p[0], x0), x1)
	return math.Hypot(dx, p[1]-y) - r
}

// cover turns a distance into pixel coverage.
func cover(d, px float64) float64 { return math.Min(math.Max(0.5-d/px, 0), 1) }

func over(dst, src color.NRGBA, a float64) color.NRGBA {
	sa := a * float64(src.A) / 255
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return color.NRGBA{}
	}
	mix := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*sa + float64(d)*da*(1-sa)) / oa))
	}
	return color.NRGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(math.Round(oa * 255))}
}
