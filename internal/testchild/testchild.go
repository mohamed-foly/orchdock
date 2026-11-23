// Package testchild is a small cross-platform command used by tests of hosts
// and runners. A test binary starts itself as this command by setting
// environment variables, so no helper program has to be built.
package testchild

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	"golang.org/x/term"
)

// Environment variables that select the role of a test binary.
const (
	EnvRole = "ORCHDOCK_TEST_ROLE"
	EnvDir  = "ORCHDOCK_TEST_DIR"
	EnvMode = "ORCHDOCK_TEST_MODE"
	EnvArg  = "ORCHDOCK_TEST_ARG"
)

// Main is the command a host runs in tests.
//
//	echo        prints "ready", then "echo:<line>" for each input line
//	exit N      prints "bye" and exits with code N
//	sleep       prints "sleeping" and waits
//	spam N      prints about N bytes of numbered lines, then "SPAM-DONE"
//	trap        ignores the first Ctrl+C ("got interrupt 1"), exits on the second
//	tree        starts a sleeping grandchild, prints "grandchild:<pid>", waits
//	size        prints "size:<cols>x<rows>" whenever the terminal size changes
//	bell        prints a title update ended by BEL, then one real bell
//	listen P    serves HTTP on 127.0.0.1:P ("ok" on /), prints "listening:P"
func Main(mode, arg string) {
	switch mode {
	case "late-exit": // runs for 1.5 s, then exits with code N
		n, _ := strconv.Atoi(arg)
		fmt.Print("up\r\n")
		time.Sleep(1500 * time.Millisecond)
		os.Exit(n)
	case "listen":
		ln, err := net.Listen("tcp", "127.0.0.1:"+arg)
		if err != nil {
			fmt.Printf("listen-failed:%v\r\n", err)
			os.Exit(7)
		}
		fmt.Printf("listening:%s\r\n", arg)
		mux := http.NewServeMux()
		mux.HandleFunc("/static/app-icon.png", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyPNG)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<!doctype html><html><head><link rel="stylesheet" href="/s.css"><link rel="icon" type="image/png" href="static/app-icon.png"></head><body>ok</body></html>`))
		})
		_ = http.Serve(ln, mux) //nolint:gosec // a test helper on loopback
	case "echo":
		fmt.Print("ready\r\n")
		var line []byte
		buf := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				return
			}
			if buf[0] == '\r' || buf[0] == '\n' {
				fmt.Printf("echo:%s\r\n", line)
				line = line[:0]
				continue
			}
			line = append(line, buf[0])
		}
	case "exit":
		n, _ := strconv.Atoi(arg)
		fmt.Print("bye\r\n")
		os.Exit(n)
	case "sleep":
		fmt.Print("sleeping\r\n")
		time.Sleep(10 * time.Minute)
	case "spam":
		n, _ := strconv.Atoi(arg)
		var b bytes.Buffer
		for i := 0; b.Len() < n; i++ {
			fmt.Fprintf(&b, "line %08d\r\n", i)
		}
		os.Stdout.Write(b.Bytes())
		fmt.Print("SPAM-DONE\r\n")
	case "trap":
		sig := make(chan os.Signal, 4)
		signal.Notify(sig, os.Interrupt)
		fmt.Print("trapping\r\n")
		<-sig
		fmt.Print("got interrupt 1\r\n")
		<-sig
		fmt.Print("got interrupt 2\r\n")
	case "tree":
		cmd := exec.Command(os.Args[0]) //nolint:gosec // the test binary starts itself as a sleeping child
		cmd.Env = append(os.Environ(), EnvMode+"=sleep")
		if err := cmd.Start(); err != nil {
			fmt.Print("tree-failed\r\n")
			os.Exit(9)
		}
		fmt.Printf("grandchild:%d\r\n", cmd.Process.Pid)
		time.Sleep(10 * time.Minute)
	case "size":
		lastW, lastH := -1, -1
		for {
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && (w != lastW || h != lastH) {
				lastW, lastH = w, h
				fmt.Printf("size:%dx%d;\r\n", w, h)
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "bell":
		fmt.Print("\x1b]0;a title\a")
		time.Sleep(100 * time.Millisecond)
		fmt.Print("ding\a\r\n")
		time.Sleep(10 * time.Minute)
	default:
		fmt.Print("unknown mode\r\n")
		os.Exit(8)
	}
}

// tinyPNG is a 1x1 PNG served as the icon of the "listen" mode's page.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4,
	0x89, 0, 0, 0, 0x0d, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0x60, 0x60, 0x60, 0xf8, 0x0f, 0, 0x01, 0x04, 0x01, 0, 0x5f, 0x2b, 0x55, 0x0a,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}
