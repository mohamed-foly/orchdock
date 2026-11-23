package main

import "testing"

func TestNumericVersion(t *testing.T) {
	for in, want := range map[string]string{"v1.2.3": "1.2.3.0", "1.0.10-rc1": "1.0.10.0", "dev": "0.0.0.0", "v1.2": "0.0.0.0", "v1.x.3": "0.0.0.0"} {
		if got := numericVersion(in); got != want {
			t.Errorf("numericVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPlanNames(t *testing.T) {
	win := plan("windows", "gui")
	if len(win) != 2 || win[0].name != "orchdock.exe" || win[1].name != "orchdockw.exe" {
		t.Fatalf("windows plan: %+v", win)
	}
	if p := plan("linux", "server"); len(p) != 1 || p[0].name != "orchdock" || p[0].cgo {
		t.Fatalf("server plan: %+v", p)
	}
}

func TestAppIconOpaqueCentre(t *testing.T) {
	for _, n := range []int{16, 256} {
		img := appIcon(n, false)
		if img.Bounds().Dx() != n || img.NRGBAAt(n/2, n-2).A == 0 || img.NRGBAAt(0, 0).A == 255 {
			t.Fatalf("icon %d px looks wrong", n)
		}
	}
}
