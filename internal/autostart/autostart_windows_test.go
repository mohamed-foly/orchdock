package autostart

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestSetAndEnabledUseOnlyTheirOwnValue(t *testing.T) {
	runKey = `Software\OrchDockTest\Run` // never the real sign-in list
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\OrchDockTest\Run`)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\OrchDockTest`)
	})
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	_ = k.SetStringValue("SomethingElse", "other.exe")
	k.Close()

	if on, _, err := Enabled(); err != nil || on {
		t.Fatalf("before: %v %v", on, err)
	}
	if err := Set(true, `C:\Tools\OrchDock\orchdockw.exe`); err != nil {
		t.Fatal(err)
	}
	on, cmd, err := Enabled()
	if err != nil || !on || cmd != `"C:\Tools\OrchDock\orchdockw.exe" ui --tray` || strings.Contains(cmd, `\\`) {
		t.Fatalf("after on: %v %q %v", on, cmd, err)
	}
	if err := Set(false, ""); err != nil {
		t.Fatal(err)
	}
	if err := Set(false, ""); err != nil {
		t.Fatalf("turning off twice: %v", err)
	}
	if on, _, _ := Enabled(); on {
		t.Fatal("still on")
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	defer k.Close()
	if v, _, err := k.GetStringValue("SomethingElse"); err != nil || v != "other.exe" {
		t.Fatalf("another program's entry was touched: %q %v", v, err)
	}
}
