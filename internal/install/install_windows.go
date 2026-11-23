//go:build windows

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/mohamed-foly/orchdock/internal/autostart"
)

// Supported reports whether Install works on this system.
const Supported = true

// Where things go; variables so tests can point them somewhere harmless.
var (
	envKey       = `Environment`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\OrchDock`
	startMenuDir = func() string {
		return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
	}
	notifyEnvironment = broadcastEnvironment
	autostartEnabled  = autostart.Enabled
	autostartSet      = autostart.Set
)

// DefaultDir is the per-user program folder.
func DefaultDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OrchDock")
}

// shortcut is the Start-menu entry.
func shortcut() string { return filepath.Join(startMenuDir(), "OrchDock.lnk") }

// InstalledAt returns the program folder of an existing installation.
func InstalledAt() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	dir, _, err := k.GetStringValue("InstallLocation")
	return dir, err == nil && dir != ""
}

// Install copies the programs to o.To and registers them with Windows.
func Install(o Options) Report {
	var r Report
	if err := os.MkdirAll(o.To, 0o755); err != nil {
		r.add("create "+o.To, err)
		return r
	}
	removeAside(o.To)
	for _, p := range Programs {
		r.add("copy "+exe(p), copyProgram(filepath.Join(o.From, exe(p)), filepath.Join(o.To, exe(p))))
	}
	if r.Err() != nil {
		return r
	}
	r.add("add the alias "+Alias, linkAlias(o.To))
	envFile := filepath.Join(o.To, ".env")
	if src := filepath.Join(o.From, ".env"); !same(o.From, o.To) {
		if _, err := os.Stat(envFile); os.IsNotExist(err) {
			if b, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(envFile, b, 0o600) // keep the settings of a portable copy
			}
		}
	}
	r.add("keep the data in "+o.Home, SetEnvLine(envFile, "ORCHDOCK_HOME", o.Home))
	tray := filepath.Join(o.To, exe("orchdockw"))
	r.add("add OrchDock to the Start menu", makeShortcut(shortcut(), tray, "ui", o.To))
	added, err := addToPath(o.To)
	if added || err != nil {
		r.add("add "+o.To+" to your PATH", err)
	}
	on, cmd, _ := autostartEnabled()
	switch {
	case o.Autostart:
		r.add("start OrchDock in the tray at sign-in", autostartSet(true, tray))
	case on && !strings.Contains(strings.ToLower(cmd), strings.ToLower(o.To)):
		r.add("move the start at sign-in to the installed program", autostartSet(true, tray))
	}
	r.add("list OrchDock in Apps (for uninstalling)", writeUninstallEntry(o))
	notifyEnvironment()
	return r
}

// Uninstall removes everything Install added except the data and the program
// files themselves, which a running program cannot delete; it returns those
// files for DeleteLater.
func Uninstall(dir string) (Report, []string) {
	var r Report
	if on, cmd, _ := autostartEnabled(); on && strings.Contains(strings.ToLower(cmd), strings.ToLower(dir)) {
		r.add("stop starting at sign-in", autostartSet(false, ""))
	}
	if err := os.Remove(shortcut()); err == nil || !os.IsNotExist(err) {
		r.add("remove the Start-menu entry", err)
	}
	if removed, err := removeFromPath(dir); removed || err != nil {
		r.add("remove "+dir+" from your PATH", err)
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err == nil || !errors.Is(err, registry.ErrNotExist) {
		r.add("remove OrchDock from Apps", err)
	}
	notifyEnvironment()
	var files []string
	for _, n := range append(append([]string{}, Programs...), Alias) {
		files = append(files, filepath.Join(dir, exe(n)))
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.old*"))
	files = append(files, old...)
	files = append(files, filepath.Join(dir, ".env"))
	return r, files
}

// DeleteLater deletes files, then dir when it is empty, a few seconds after
// this program has exited (a running program cannot delete itself).
func DeleteLater(dir string, files []string) error {
	var b strings.Builder
	b.WriteString("ping -n 4 127.0.0.1 >nul")
	for _, f := range files {
		fmt.Fprintf(&b, ` & del /f /q "%s" 2>nul`, f)
	}
	fmt.Fprintf(&b, ` & rd "%s" 2>nul`, dir) // not /s: only when nothing else is left
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /s /c "` + b.String() + `"`,
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS,
	}
	return cmd.Start()
}

// linkAlias makes odock.exe a hard link to orchdock.exe (a copy where links
// are not possible).
func linkAlias(dir string) error {
	dst, src := filepath.Join(dir, exe(Alias)), filepath.Join(dir, exe("orchdock"))
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		if err := os.Rename(dst, asideName(dst)); err != nil {
			return err
		}
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return copyProgram(src, dst)
}

// makeShortcut writes a .lnk file through the Windows Script Host.
func makeShortcut(lnk, target, args, workDir string) error {
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		var oe *ole.OleError
		if !errors.As(err, &oe) || oe.Code() != 1 { // S_FALSE: already initialised
			return err
		}
	}
	defer ole.CoUninitialize()
	unk, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return err
	}
	defer unk.Release()
	sh, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer sh.Release()
	v, err := oleutil.CallMethod(sh, "CreateShortcut", lnk)
	if err != nil {
		return err
	}
	sc := v.ToIDispatch()
	defer sc.Release()
	for k, val := range map[string]string{
		"TargetPath": target, "Arguments": args, "WorkingDirectory": workDir,
		"IconLocation": target + ",0", "Description": "OrchDock",
	} {
		if _, err := oleutil.PutProperty(sc, k, val); err != nil {
			return err
		}
	}
	_, err = oleutil.CallMethod(sc, "Save")
	return err
}

// --- user PATH -------------------------------------------------------------

func pathEntries() (registry.Key, []string, uint32, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return 0, nil, 0, err
	}
	v, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return k, nil, registry.EXPAND_SZ, nil
	}
	if err != nil {
		k.Close()
		return 0, nil, 0, err
	}
	var parts []string
	for _, p := range strings.Split(v, ";") {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return k, parts, typ, nil
}

func samePathEntry(entry, dir string) bool {
	x, err := registry.ExpandString(entry)
	if err != nil {
		x = entry
	}
	return same(strings.TrimRight(x, `\/`), strings.TrimRight(dir, `\/`))
}

func writePath(k registry.Key, parts []string, typ uint32) error {
	v := strings.Join(parts, ";")
	if typ == registry.SZ {
		return k.SetStringValue("Path", v)
	}
	return k.SetExpandStringValue("Path", v)
}

// addToPath adds dir to the user PATH; false when it was already there.
func addToPath(dir string) (bool, error) {
	k, parts, typ, err := pathEntries()
	if err != nil {
		return false, err
	}
	defer k.Close()
	for _, p := range parts {
		if samePathEntry(p, dir) {
			return false, nil
		}
	}
	return true, writePath(k, append(parts, dir), typ)
}

// removeFromPath takes dir off the user PATH; false when it was not there.
func removeFromPath(dir string) (bool, error) {
	k, parts, typ, err := pathEntries()
	if err != nil {
		return false, err
	}
	defer k.Close()
	keep := parts[:0:0]
	for _, p := range parts {
		if !samePathEntry(p, dir) {
			keep = append(keep, p)
		}
	}
	if len(keep) == len(parts) {
		return false, nil
	}
	return true, writePath(k, keep, typ)
}

// broadcastEnvironment tells Explorer the user environment changed, so new
// terminals see the new PATH without signing out.
func broadcastEnvironment() {
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var res uintptr
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 3000, uintptr(unsafe.Pointer(&res)))
}

// --- Apps & features entry ---------------------------------------------------

func writeUninstallEntry(o Options) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cli := filepath.Join(o.To, exe("orchdock"))
	var size uint32
	for _, p := range Programs {
		if st, err := os.Stat(filepath.Join(o.To, exe(p))); err == nil {
			size += uint32(st.Size() / 1024)
		}
	}
	for name, v := range map[string]string{
		"DisplayName":     "OrchDock",
		"DisplayIcon":     filepath.Join(o.To, exe("orchdockw")) + ",0",
		"DisplayVersion":  o.Version,
		"Publisher":       "OrchDock contributors",
		"InstallLocation": o.To,
		"UninstallString": fmt.Sprintf(`"%s" uninstall --yes`, cli),
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for name, v := range map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": size} {
		if err := k.SetDWordValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

// --- program files -----------------------------------------------------------

// exe adds the platform's program suffix.
func exe(name string) string { return name + ".exe" }

// copyProgram copies src to dst. A program that is running cannot be
// overwritten on Windows but can be renamed, so an existing dst is moved
// aside first (removeAside cleans those up later).
func copyProgram(src, dst string) error {
	if same(src, dst) {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		if err := os.Remove(dst); err != nil {
			if err := os.Rename(dst, asideName(dst)); err != nil {
				return err
			}
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// asideName is a free name next to p for a program moved out of the way.
func asideName(p string) string {
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s.old%d", p, i)
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
}

// removeAside deletes programs moved aside earlier that are no longer in use.
func removeAside(dir string) {
	old, _ := filepath.Glob(filepath.Join(dir, "*.old*"))
	for _, p := range old {
		_ = os.Remove(p)
	}
}

func same(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(x), filepath.Clean(y))
}
