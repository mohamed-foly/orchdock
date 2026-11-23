package paths

import (
	"path/filepath"
	"testing"
)

func env(m map[string]string) Env { return func(k string) string { return m[k] } }

func TestResolveHome(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	tests := []struct {
		name string
		in   HomeInput
		want string
	}{
		{"flag wins", HomeInput{Flag: "/x/flag", Env: env(map[string]string{EnvHome: "/x/env"}), ExePath: "/opt/app/orchdock", GOOS: "linux", Writable: yes, Exists: yes}, "/x/flag"},
		{"env next", HomeInput{Env: env(map[string]string{EnvHome: "/x/env"}), ExePath: "/srv/app/orchdock", GOOS: "linux", Writable: yes, Exists: yes}, "/x/env"},
		{"existing data dir next to exe", HomeInput{Env: env(nil), ExePath: "/usr/bin/orchdock", GOOS: "linux", Writable: no, Exists: yes}, "/usr/bin/data"},
		{"writable portable dir", HomeInput{Env: env(nil), ExePath: "/srv/tools/od/orchdock", GOOS: "linux", Writable: yes, Exists: no}, "/srv/tools/od/data"},
		{"not writable falls back to per-user", HomeInput{Env: env(map[string]string{"HOME": "/home/u"}), ExePath: "/srv/ro/orchdock", GOOS: "linux", Writable: no, Exists: no}, "/home/u/.local/share/orchdock"},
		{"system location falls back to per-user", HomeInput{Env: env(map[string]string{"HOME": "/home/u"}), ExePath: "/usr/local/bin/orchdock", GOOS: "linux", Writable: yes, Exists: no}, "/home/u/.local/share/orchdock"},
		{"go run temp dir falls back", HomeInput{Env: env(map[string]string{"XDG_DATA_HOME": "/xdg"}), ExePath: "/tmp/go-build1/exe/orchdock", GOOS: "linux", TempDir: "/tmp", Writable: yes, Exists: no}, "/xdg/orchdock"},
		{"windows per-user", HomeInput{Env: env(map[string]string{"LOCALAPPDATA": "/w/local"}), ExePath: "/w/Program Files/OrchDock/orchdock.exe", GOOS: "windows", Writable: yes, Exists: no}, "/w/local/orchdock"},
		{"darwin per-user", HomeInput{Env: env(map[string]string{"HOME": "/Users/u"}), ExePath: "/Applications/OrchDock.app/Contents/MacOS/orchdock", GOOS: "darwin", Writable: yes, Exists: no}, "/Users/u/Library/Application Support/orchdock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveHome(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			absWant, _ := filepath.Abs(tt.want)
			if NormKey(got) != NormKey(tt.want) && NormKey(got) != NormKey(absWant) {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestResolveHomeNoPerUserDir(t *testing.T) {
	_, err := ResolveHome(HomeInput{Env: env(nil), GOOS: "linux", Writable: func(string) bool { return false }, Exists: func(string) bool { return false }})
	if err == nil {
		t.Fatal("expected an error without HOME and XDG_DATA_HOME")
	}
}

func TestNormKey(t *testing.T) {
	tests := map[string]string{
		`X:\Work\App\login-page\`: "x:/work/app/login-page",
		`X:/work//app/Login-Page`: "x:/work/app/login-page",
		`x:\\work\\app`:           "x:/work/app",
		"/home/u/Profile/":        "/home/u/Profile",
		"/":                       "/",
	}
	for in, want := range tests {
		if got := NormKey(in); got != want {
			t.Errorf("NormKey(%q)=%q want %q", in, got, want)
		}
	}
	if !SamePath(`X:\Data\x\Local\p`, "x:/data/X/local/P/") {
		t.Error("SamePath should ignore case and separators on drive paths")
	}
}

func TestVariants(t *testing.T) {
	got := Variants(`X:\a\b`)
	want := []string{"X:/a/b", `X:\a\b`, `X:\\a\\b`}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("variant %d = %q want %q", i, got[i], want[i])
		}
	}
	if v := Variants("/home/u/p"); len(v) != 3 {
		t.Errorf("posix variants: %v", v)
	}
}

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Shop admin (staging)": "shop-admin-staging",
		"  --Hello__World--  ": "hello-world",
		"تجربة":                "item",
		"web-api-2":            "web-api-2",
	}
	for in, want := range tests {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	if !IsWithin(`X:\home\projects\a`, `x:/home`) || IsWithin(`X:\homework`, `X:\home`) {
		t.Error("IsWithin boundary check failed")
	}
}
