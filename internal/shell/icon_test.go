package shell

import (
	"bytes"
	"image/png"
	"testing"
)

func TestMarkIsAValidTransparentImageWithTheMarkInIt(t *testing.T) {
	for _, n := range []int{16, 32, 64, 256} {
		for _, badge := range []Badge{BadgeNone, BadgeRunning} {
			img, err := png.Decode(bytes.NewReader(PNG(n, badge)))
			if err != nil {
				t.Fatalf("%d: %v", n, err)
			}
			if b := img.Bounds(); b.Dx() != n || b.Dy() != n {
				t.Fatalf("%d: size %v", n, b)
			}
			if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
				t.Errorf("%d: the corner must be transparent", n)
			}
			if _, _, _, a := img.At(n/2, n*58/128).RGBA(); a == 0 {
				t.Errorf("%d: the middle panel must be drawn", n)
			}
		}
	}
}

func TestRunningBadgeChangesTheBottomRightOnly(t *testing.T) {
	idle := Mark(64, BadgeNone)
	run := Mark(64, BadgeRunning)
	if idle.NRGBAAt(48, 48) == run.NRGBAAt(48, 48) {
		t.Fatal("the badge must change the bottom right")
	}
	if idle.NRGBAAt(32, 24) != run.NRGBAAt(32, 24) {
		t.Fatal("the rest of the icon must not change")
	}
}
