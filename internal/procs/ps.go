package procs

import (
	"path/filepath"
	"strconv"
	"strings"
)

// parsePS parses `ps -axww -o pid=,ppid=,command=` output.
func parsePS(out string, names []string) []Process {
	match := nameFilter(names)
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	var res []Process
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		cmd := strings.TrimSpace(strings.SplitN(strings.Join(f, " "), " ", 3)[2])
		exe := Tokenize(cmd)
		if len(exe) == 0 {
			continue
		}
		name := filepath.Base(filepath.ToSlash(exe[0]))
		if !match(name) && !matchesBundle(cmd, want) {
			continue
		}
		res = append(res, Process{PID: pid, PPID: ppid, Name: name, CommandLine: cmd})
	}
	return res
}

// matchesBundle matches macOS executable names that contain spaces.
func matchesBundle(cmd string, want map[string]bool) bool {
	lc := strings.ToLower(cmd)
	for n := range want {
		if strings.Contains(n, " ") && (strings.Contains(lc, "/"+n+" ") || strings.HasSuffix(lc, "/"+n)) {
			return true
		}
	}
	return false
}
