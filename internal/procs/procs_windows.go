//go:build windows

package procs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCommandLineInformation: NtQueryInformationProcess class (Windows 8.1+).
const processCommandLineInformation = 60

// stillActive is the exit code GetExitCodeProcess reports for a running process.
const stillActive = 259

type winLister struct{}

// New returns the Windows Lister: native APIs, falling back to CIM.
func New() Lister { return winLister{} }

// List implements Lister.
func (winLister) List(ctx context.Context, names []string) ([]Process, error) {
	out, err := listNative(names)
	if err == nil {
		return out, nil
	}
	return listCIM(ctx, names)
}

func listNative(names []string) ([]Process, error) {
	match := nameFilter(names)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, err
	}
	var out []Process
	for {
		name := windows.UTF16ToString(e.ExeFile[:])
		if e.ProcessID != 0 && match(name) {
			// Protected processes refuse the query; keep them without a command line.
			cmd, _ := commandLine(e.ProcessID)
			out = append(out, Process{PID: int(e.ProcessID), PPID: int(e.ParentProcessID), Name: name, CommandLine: cmd})
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out, nil
}

func commandLine(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	size := uint32(4096)
	for i := 0; i < 4; i++ {
		buf := make([]byte, size)
		var ret uint32
		err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), size, &ret)
		if err == nil {
			us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
			return us.String(), nil
		}
		if ret > size {
			size = ret
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("command line too large for pid %d", pid)
}

func listCIM(ctx context.Context, names []string) ([]Process, error) {
	filter := ""
	if len(names) > 0 {
		var filters []string
		for _, n := range names {
			filters = append(filters, fmt.Sprintf("Name='%s'", strings.ReplaceAll(n, "'", "")))
		}
		filter = fmt.Sprintf(` -Filter "%s"`, strings.Join(filters, " or "))
	}
	script := fmt.Sprintf(`$p = Get-CimInstance Win32_Process%s | Select-Object ProcessId,ParentProcessId,Name,CommandLine; ConvertTo-Json -InputObject @($p) -Compress`, filter)
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ProcessID       int    `json:"ProcessId"`
		ParentProcessID int    `json:"ParentProcessId"`
		Name            string `json:"Name"`
		CommandLine     string `json:"CommandLine"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(rows))
	for _, r := range rows {
		out = append(out, Process{PID: r.ProcessID, PPID: r.ParentProcessID, Name: r.Name, CommandLine: r.CommandLine})
	}
	return out, nil
}

// Alive reports whether pid is a running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// The process exists but belongs to someone we may not query.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
