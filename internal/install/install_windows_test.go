//go:build windows

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// sandbox points every system change at test locations: a test registry key,
// a temporary Start menu and fake sign-in settings. Nothing real is touched.
func sandbox(t *testing.T) (startMenu string, auto *string) {
	t.Helper()
	const root = `Software\OrchDockInstallTest`
	envKey, uninstallKey = root+`\Environment`, root+`\Uninstall`
	startMenu = t.TempDir()
	startMenuDir = func() string { return startMenu }
	notifyEnvironment = func() {}
	cmd := ""
	auto = &cmd
	autostartEnabled = func() (bool, string, error) { return cmd != "", cmd, nil }
	autostartSet = func(on bool, exe string) error {
		cmd = ""
		if on {
			cmd = `"` + exe + `" ui --tray`
		}
		return nil
	}
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, root+`\Environment`)
		_ = registry.DeleteKey(registry.CURRENT_USER, root+`\Uninstall`)
		_ = registry.DeleteKey(registry.CURRENT_USER, root)
	})
	return startMenu, auto
}

func fakeRelease(t *testing.T) string {
	t.Helper()
	from := t.TempDir()
	for _, p := range Programs {
		if err := os.WriteFile(filepath.Join(from, exe(p)), []byte("program "+p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(from, ".env"), []byte("ORCHDOCK_TERMINAL=wt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return from
}

func userPath(t *testing.T) string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("Path")
	return v
}

func TestInstallAndUninstallUndoEachOther(t *testing.T) {
	startMenu, auto := sandbox(t)
	k, _, _ := registry.CreateKey(registry.CURRENT_USER, envKey, registry.SET_VALUE)
	_ = k.SetExpandStringValue("Path", `%USERPROFILE%\bin;C:\Other`)
	k.Close()

	from, to := fakeRelease(t), filepath.Join(t.TempDir(), "OrchDock")
	home := filepath.Join(t.TempDir(), "data")
	rep := Install(Options{From: from, To: to, Home: home, Version: "v1.2.3", Autostart: true})
	if err := rep.Err(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"orchdock.exe", "orchdockw.exe", "odock.exe"} {
		if _, err := os.Stat(filepath.Join(to, n)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	env, _ := os.ReadFile(filepath.Join(to, ".env"))
	if !strings.Contains(string(env), "ORCHDOCK_TERMINAL=wt") || !strings.Contains(string(env), "ORCHDOCK_HOME="+home) {
		t.Fatalf(".env keeps the portable settings and points at the data: %q", env)
	}
	if _, err := os.Stat(filepath.Join(startMenu, "OrchDock.lnk")); err != nil {
		t.Fatalf("Start-menu entry: %v", err)
	}
	if p := userPath(t); !strings.HasSuffix(p, ";"+to) || !strings.HasPrefix(p, `%USERPROFILE%\bin;`) {
		t.Fatalf("PATH: %q", p)
	}
	if !strings.Contains(*auto, filepath.Join(to, "orchdockw.exe")) {
		t.Fatalf("sign-in: %q", *auto)
	}
	if d, ok := InstalledAt(); !ok || d != to {
		t.Fatalf("InstalledAt: %q %v", d, ok)
	}

	// installing again changes nothing twice
	if err := Install(Options{From: from, To: to, Home: home}).Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(userPath(t), to) != 1 {
		t.Fatalf("PATH has the folder twice: %q", userPath(t))
	}

	rep, files := Uninstall(to)
	if err := rep.Err(); err != nil {
		t.Fatal(err)
	}
	if p := userPath(t); p != `%USERPROFILE%\bin;C:\Other` {
		t.Fatalf("PATH after: %q", p)
	}
	if _, err := os.Stat(filepath.Join(startMenu, "OrchDock.lnk")); !os.IsNotExist(err) {
		t.Fatal("the Start-menu entry is still there")
	}
	if *auto != "" {
		t.Fatalf("sign-in still set: %q", *auto)
	}
	if _, ok := InstalledAt(); ok {
		t.Fatal("still listed in Apps")
	}
	if len(files) < 4 {
		t.Fatalf("files to delete: %v", files)
	}
}

func TestUninstallLeavesAnotherProgramsSignInAlone(t *testing.T) {
	_, auto := sandbox(t)
	*auto = `"C:\Portable\orchdockw.exe" ui --tray`
	Uninstall(filepath.Join(t.TempDir(), "OrchDock"))
	if *auto == "" {
		t.Fatal("a start at sign-in of another copy must stay")
	}
}

func TestRunningProgramIsMovedAside(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "orchdockw.exe")
	src := filepath.Join(dir, "new.exe")
	ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Skip("no ping.exe to stand in for a running program")
	}
	_ = os.WriteFile(dst, ping, 0o755)
	_ = os.WriteFile(src, []byte("new"), 0o755)
	running := exec.Command(dst, "-n", "30", "127.0.0.1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Process.Kill(); _ = running.Wait() })
	if err := copyProgram(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("got %q", b)
	}
	if old, _ := filepath.Glob(dst + ".old*"); len(old) != 1 {
		t.Fatalf("the running program should be moved aside: %v", old)
	}
}

func TestSetEnvLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(p, []byte("# settings\nORCHDOCK_HOME=old\nOTHER=1\n"), 0o600)
	if err := SetEnvLine(p, "ORCHDOCK_HOME", `D:\data`); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "# settings\nORCHDOCK_HOME=D:\\data\nOTHER=1\n" {
		t.Fatalf("got %q", b)
	}
	q := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(q, []byte("ORCHDOCK_HOME=x\n"), 0o600)
	_ = SetEnvLine(q, "ORCHDOCK_HOME", "")
	if _, err := os.Stat(q); !os.IsNotExist(err) {
		t.Fatal("an empty .env should be removed")
	}
}
