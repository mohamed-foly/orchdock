package procs

import "testing"

func TestParsePS(t *testing.T) {
	out := `    1     0 /sbin/init splash
  412     1 /usr/bin/node /srv/app/server.js --port 7401
  413   412 /Applications/Some App.app/Contents/MacOS/Some App --flag
  bad line
  414     1 node worker.js
`
	all := parsePS(out, nil)
	if len(all) != 4 {
		t.Fatalf("want 4 processes, got %+v", all)
	}
	nodes := parsePS(out, []string{"NODE"})
	if len(nodes) != 2 || nodes[0].PID != 412 || nodes[0].PPID != 1 || nodes[0].CommandLine != "/usr/bin/node /srv/app/server.js --port 7401" {
		t.Fatalf("node filter: %+v", nodes)
	}
	app := parsePS(out, []string{"Some App"})
	if len(app) != 1 || app[0].PID != 413 {
		t.Fatalf("bundle name match: %+v", app)
	}
}
