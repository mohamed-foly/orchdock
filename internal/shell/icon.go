// Package shell holds what every window shell shares: the application icon.
package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Colours of the mark.
var (
	panelSide = color.NRGBA{0x28, 0x44, 0x51, 0xFF}
	panelMid  = color.NRGBA{0x21, 0x3B, 0x48, 0xFF}
	edge      = color.NRGBA{0x80, 0xDA, 0xC6, 0xFF}
	cream     = color.NRGBA{0xF6, 0xF6, 0xEA, 0xFF}
	dot       = color.NRGBA{0x8D, 0xE4, 0xC1, 0xFF}
	runGreen  = color.NRGBA{0x1F, 0xB8, 0x6E, 0xFF}
	ringWhite = color.NRGBA{0xF4, 0xF6, 0xF4, 0xFF}
)

// Badge is a small status mark at the bottom right of the icon.
type Badge int

// Badges.
const (
	BadgeNone Badge = iota
	BadgeRunning
)

// TrayIcon is the 32 px tray icon as PNG: a green play badge while
// something runs, and an amber dot when an item needs the user.
func TrayIcon(running, attention bool) []byte {
	b := BadgeNone
	if running {
		b = BadgeRunning
	}
	img := Mark(32, b)
	if attention {
		attentionDot(img)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

var amber = color.NRGBA{0xF5, 0x9E, 0x0B, 0xFF}

// attentionDot paints an amber dot with a light ring at the top right.
func attentionDot(img *image.NRGBA) {
	n := img.Bounds().Dx()
	px := 128 / float64(n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			p := [2]float64{(float64(x) + 0.5) * px, (float64(y) + 0.5) * px}
			d := math.Hypot(p[0]-104, p[1]-24)
			c := over(img.NRGBAAt(x, y), ringWhite, cover(d-22, px))
			img.SetNRGBA(x, y, over(c, amber, cover(d-17, px)))
		}
	}
}

// PNG encodes Mark as PNG.
func PNG(n int, badge Badge) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, Mark(n, badge))
	return buf.Bytes()
}

// Mark draws the OrchDock mark on a transparent n×n canvas: three terminal
// panels standing in a dock. The shapes are the 128-unit design, drawn with
// signed distances so they stay sharp at any size.
func Mark(n int, badge Badge) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	px := 128 / float64(n) // design units per pixel
	dock := flatten([][2]float64{{16, 85}, {16, 94}}, [][3][2]float64{{{16, 94}, {16, 105}, {27, 105}}}, [][2]float64{{101, 105}}, [][3][2]float64{{{101, 105}, {112, 105}, {112, 94}}}, [][2]float64{{112, 85}})
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			p := [2]float64{(float64(x) + 0.5) * px, (float64(y) + 0.5) * px}
			var c color.NRGBA
			c = panel(c, p, 19, 24, 51, 76, 7, panelSide, px)
			c = panel(c, p, 48, 15, 88, 80, 8, panelMid, px)
			c = panel(c, p, 85, 29, 109, 76, 6, panelSide, px)
			c = over(c, cream, cover(polylineDist(p, [][2]float64{{58, 43}, {66, 50}, {58, 57}})-2.25, px))
			c = over(c, cream, cover(segDist(p, [2]float64{69, 59}, [2]float64{77, 59})-2, px))
			c = over(c, dot, cover(math.Hypot(p[0]-35, p[1]-62)-3.5, px))
			c = over(c, dot, cover(math.Hypot(p[0]-97, p[1]-62)-3.5, px))
			c = over(c, edge, cover(polylineDist(p, dock)-4, px)) // the dock: a stroke of width 8
			if badge == BadgeRunning {
				c = badgeRunning(c, p, px)
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// panel draws a rounded rectangle with the mark's fill and edge.
func panel(c color.NRGBA, p [2]float64, x0, y0, x1, y1, r float64, fill color.NRGBA, px float64) color.NRGBA {
	d := roundBox(p, (x0+x1)/2, (y0+y1)/2, (x1-x0)/2, (y1-y0)/2, r)
	c = over(c, fill, cover(d, px))
	return over(c, edge, cover(math.Abs(d)-1.5, px)) // stroke width 3, centred on the outline
}

// badgeRunning is a green disc with a white triangle, bottom right.
func badgeRunning(c color.NRGBA, p [2]float64, px float64) color.NRGBA {
	cx, cy, r := 98.0, 98.0, 24.0
	c = over(c, ringWhite, cover(math.Hypot(p[0]-cx, p[1]-cy)-r, px))
	c = over(c, runGreen, cover(math.Hypot(p[0]-cx, p[1]-cy)-(r-5), px))
	// right-pointing triangle
	a, b, t := [2]float64{cx - 6, cy - 9.5}, [2]float64{cx - 6, cy + 9.5}, [2]float64{cx + 10, cy}
	return over(c, ringWhite, cover(triDist(p, a, b, t), px))
}

// flatten turns a path of lines and quadratic curves into points.
func flatten(start [][2]float64, q1 [][3][2]float64, line [][2]float64, q2 [][3][2]float64, end [][2]float64) [][2]float64 {
	pts := append([][2]float64(nil), start...)
	curve := func(q [3][2]float64) {
		for i := 1; i <= 12; i++ {
			t := float64(i) / 12
			u := 1 - t
			pts = append(pts, [2]float64{u*u*q[0][0] + 2*u*t*q[1][0] + t*t*q[2][0], u*u*q[0][1] + 2*u*t*q[1][1] + t*t*q[2][1]})
		}
	}
	for _, q := range q1 {
		curve(q)
	}
	pts = append(pts, line...)
	for _, q := range q2 {
		curve(q)
	}
	return append(pts, end...)
}

func polylineDist(p [2]float64, pts [][2]float64) float64 {
	d := math.Inf(1)
	for i := 1; i < len(pts); i++ {
		d = math.Min(d, segDist(p, pts[i-1], pts[i]))
	}
	return d
}

func segDist(p, a, b [2]float64) float64 {
	abx, aby := b[0]-a[0], b[1]-a[1]
	t := ((p[0]-a[0])*abx + (p[1]-a[1])*aby) / (abx*abx + aby*aby)
	t = math.Min(math.Max(t, 0), 1)
	return math.Hypot(p[0]-(a[0]+t*abx), p[1]-(a[1]+t*aby))
}

// triDist is the signed distance to a triangle (negative inside).
func triDist(p, a, b, c [2]float64) float64 {
	cross := func(u, v, w [2]float64) float64 { return (v[0]-u[0])*(w[1]-u[1]) - (v[1]-u[1])*(w[0]-u[0]) }
	d := math.Min(segDist(p, a, b), math.Min(segDist(p, b, c), segDist(p, c, a)))
	s1, s2, s3 := cross(a, b, p), cross(b, c, p), cross(c, a, p)
	if (s1 >= 0 && s2 >= 0 && s3 >= 0) || (s1 <= 0 && s2 <= 0 && s3 <= 0) {
		return -d
	}
	return d
}

func roundBox(p [2]float64, cx, cy, hx, hy, r float64) float64 {
	qx := math.Abs(p[0]-cx) - (hx - r)
	qy := math.Abs(p[1]-cy) - (hy - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

// cover turns a signed distance into pixel coverage.
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
