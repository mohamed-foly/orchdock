package command

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mohamed-foly/orchdock/internal/domain"
)

// fakeEnv is a machine with the listed executables on its PATH.
func fakeEnv(goos string, found ...string) Env {
	return Env{
		OS:      goos,
		Environ: []string{"PATH=/bin", "Existing=old"},
		Home:    "/home/user",
		LookPath: func(name string) (string, error) {
			for _, f := range found {
				base := f[strings.LastIndexAny(f, `/\`)+1:]
				if f == name || base == name {
					return f, nil
				}
				for _, ext := range []string{".exe", ".cmd", ".bat"} { // what PATHEXT does
					if goos == "windows" && strings.TrimSuffix(base, ext) == name {
						return f, nil
					}
				}
			}
			return "", errors.New("not found")
		},
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("no such file") },
	}
}

func TestQuoteTable(t *testing.T) {
	words := []string{"plain", "has space", "it's", `say "hi"`, "$HOME", "100%", "a^b", "a&b", "back`tick", ""}
	want := map[dialect][]string{
		dialectPosix: {"plain", `'has space'`, `'it'\''s'`, `'say "hi"'`, `'$HOME'`, "100%", `'a^b'`, `'a&b'`, "'back`tick'", "''"},
		dialectPowerShell: {"plain", `'has space'`, `'it''s'`, `'say "hi"'`, `'$HOME'`, "100%", `'a^b'`, `'a&b'`,
			"'back`tick'", "''"},
		dialectCmd: {"plain", `"has space"`, `"it's"`, `"say \"hi\""`, `"$HOME"`, `"100%"`, `"a^b"`, `"a&b"`, "\"back`tick\"", `""`},
	}
	for d, exp := range want {
		for i, w := range words {
			if got := d.quote(w); got != exp[i] {
				t.Errorf("dialect %d quote(%q) = %s, want %s", d, w, got, exp[i])
			}
		}
	}
}

func TestPowerShellCallOperatorForQuotedExecutable(t *testing.T) {
	if got := dialectPowerShell.join([]string{`C:\Program Files\x\a.exe`, "arg one"}); got != `& 'C:\Program Files\x\a.exe' 'arg one'` {
		t.Fatalf("got %s", got)
	}
	if got := dialectPowerShell.join([]string{"git", "pull"}); got != "git pull" {
		t.Fatalf("got %s", got)
	}
}

func TestInsertAfterFirstWord(t *testing.T) {
	cases := []struct{ line, extra, want string }{
		{"backlog browser --port 1", "--flag", "backlog --flag browser --port 1"},
		{"tool", "--flag", "tool --flag"},
		{`"C:\Program Files\x\a.exe" run`, "--flag", `"C:\Program Files\x\a.exe" --flag run`},
		{"  'a b' c", "-x", "  'a b' -x c"},
		{"cmd arg", "", "cmd arg"},
	}
	for _, c := range cases {
		if got := insertAfterFirstWord(c.line, c.extra); got != c.want {
			t.Errorf("insertAfterFirstWord(%q,%q) = %q, want %q", c.line, c.extra, got, c.want)
		}
	}
}

func lineInput(shell, line string) Input {
	return Input{Shell: shell, Base: domain.Spec{Line: line}, Variants: domain.MergeVariants(nil, nil)}
}

func TestShellArgv(t *testing.T) {
	win := fakeEnv("windows", `C:\ps\pwsh.exe`, `C:\win\powershell.exe`, `C:\win\cmd.exe`)
	nix := fakeEnv("linux", "/bin/bash", "/bin/zsh", "/bin/sh")
	cases := []struct {
		name  string
		env   Env
		in    Input
		shell string
		want  []string
	}{
		{"pwsh line", win, lineInput("pwsh", "git pull"), "pwsh", []string{`C:\ps\pwsh.exe`, "-NoLogo", "-NoProfile", "-Command", "git pull"}},
		{"powershell with profile", win, func() Input { i := lineInput("powershell", "ls"); i.LoadProfile = true; return i }(), "powershell",
			[]string{`C:\win\powershell.exe`, "-NoLogo", "-Command", "ls"}},
		{"cmd line", win, lineInput("cmd", "dir"), "cmd", []string{`C:\win\cmd.exe`, "/d", "/s", "/c", "dir"}},
		{"auto windows prefers pwsh", win, lineInput("", "x"), "pwsh", []string{`C:\ps\pwsh.exe`, "-NoLogo", "-NoProfile", "-Command", "x"}},
		{"auto windows falls back to powershell", fakeEnv("windows", `C:\win\powershell.exe`), lineInput("auto", "x"), "powershell",
			[]string{`C:\win\powershell.exe`, "-NoLogo", "-NoProfile", "-Command", "x"}},
		{"bash line", nix, lineInput("bash", "ls -l"), "bash", []string{"/bin/bash", "-c", "ls -l"}},
		{"zsh login", nix, func() Input { i := lineInput("zsh", "ls"); i.LoadProfile = true; return i }(), "zsh", []string{"/bin/zsh", "-lic", "ls"}},
		{"interactive pwsh", win, lineInput("pwsh", ""), "pwsh", []string{`C:\ps\pwsh.exe`, "-NoLogo"}},
		{"interactive cmd", win, lineInput("cmd", ""), "cmd", []string{`C:\win\cmd.exe`, "/k"}},
		{"interactive sh", nix, lineInput("sh", ""), "sh", []string{"/bin/sh", "-l"}},
		{"shell none with a line uses auto", nix, lineInput("none", "ls"), "sh", []string{"/bin/sh", "-c", "ls"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Resolve(c.in, "", c.env)
			if err != nil {
				t.Fatal(err)
			}
			if r.Shell != c.shell || !reflect.DeepEqual(r.Argv, c.want) {
				t.Fatalf("shell %q argv %q\nwant  %q %q", r.Shell, r.Argv, c.shell, c.want)
			}
		})
	}
}

func TestAutoShellOnUnixUsesTheUsersShell(t *testing.T) {
	e := fakeEnv("linux", "/usr/bin/zsh", "/bin/sh")
	e.Environ = []string{"SHELL=/usr/bin/zsh"}
	r, err := Resolve(lineInput("", "ls"), "", e)
	if err != nil || r.Shell != "zsh" {
		t.Fatalf("%v %+v", err, r)
	}
	e.Environ = []string{"SHELL=/usr/bin/fish"} // not a supported dialect
	if r, err = Resolve(lineInput("", "ls"), "", e); err != nil || r.Shell != "sh" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestMissingShellIsAClearError(t *testing.T) {
	_, err := Resolve(lineInput("zsh", "ls"), "", fakeEnv("linux", "/bin/sh"))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func directInput(argv ...string) Input {
	return Input{Shell: "none", Base: domain.Spec{Argv: argv}, Variants: domain.MergeVariants(nil, nil)}
}

func TestDirectCommandsAndShims(t *testing.T) {
	win := fakeEnv("windows", `C:\npm\npm.cmd`, `C:\Program Files\nodejs\npx.cmd`, `C:\tools\run.ps1`, `C:\bin\git.exe`, `C:\ps\pwsh.exe`)
	cases := []struct {
		name string
		env  Env
		argv []string
		want []string
	}{
		{"exe runs directly", win, []string{"git", "pull"}, []string{`C:\bin\git.exe`, "pull"}},
		{"cmd shim goes through cmd.exe", win, []string{"npm", "run", "dev"}, []string{"cmd.exe", "/d", "/s", "/c", `C:\npm\npm.cmd run dev`}},
		{"a shim and arguments with spaces are quoted in one line", win, []string{"npx", "serve", "my site"}, []string{"cmd.exe", "/d", "/s", "/c", `"C:\Program Files\nodejs\npx.cmd" serve "my site"`}},
		{"ps1 goes through pwsh", win, []string{"run.ps1", "-x"}, []string{`C:\ps\pwsh.exe`, "-NoProfile", "-File", `C:\tools\run.ps1`, "-x"}},
		{"ps1 falls back to powershell", fakeEnv("windows", `C:\tools\run.ps1`), []string{"run.ps1"}, []string{"powershell.exe", "-NoProfile", "-File", `C:\tools\run.ps1`}},
		{"shims are a Windows thing", fakeEnv("linux", "/usr/bin/npm.cmd"), []string{"npm.cmd", "x"}, []string{"/usr/bin/npm.cmd", "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Resolve(directInput(c.argv...), "", c.env)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.Argv, c.want) || r.Shell != domain.ShellNone {
				t.Fatalf("argv %q shell %q, want %q", r.Argv, r.Shell, c.want)
			}
		})
	}
	if _, err := Resolve(directInput("definitely-missing"), "", win); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a missing executable must be ErrNotFound, got %v", err)
	}
}

func TestVariantsOnShellLines(t *testing.T) {
	in := lineInput("bash", "codex resume {sessionId}")
	in.Vars = map[string]string{"sessionId": "abc-123"}
	in.Variants = domain.MergeVariants(nil, []domain.Variant{
		{ID: "remote", AppendArgs: []string{"--remote", "ws://127.0.0.1:4810"}},
		{ID: "bypass", AppendArgs: []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{ID: "pre", PrependArgs: []string{"-c", "x=1 2"}},
		{ID: "other", Line: "echo other"},
		{ID: "both", Line: "tool run", PrependArgs: []string{"--p"}, AppendArgs: []string{"it's"}},
	})
	e := fakeEnv("linux", "/bin/bash")
	cases := []struct{ variant, want string }{
		{"", "codex resume abc-123"},
		{"default", "codex resume abc-123"},
		{"remote", "codex resume abc-123 --remote ws://127.0.0.1:4810"},
		{"bypass", "codex resume abc-123 --dangerously-bypass-approvals-and-sandbox"},
		{"pre", "codex -c 'x=1 2' resume abc-123"},
		{"other", "echo other"},
		{"both", `tool --p run 'it'\''s'`},
	}
	for _, c := range cases {
		r, err := Resolve(in, c.variant, e)
		if err != nil {
			t.Fatalf("%s: %v", c.variant, err)
		}
		if got := r.Argv[len(r.Argv)-1]; got != c.want {
			t.Errorf("variant %q: line %q, want %q", c.variant, got, c.want)
		}
	}
}

func TestVariantOnArgvCommands(t *testing.T) {
	in := directInput("tool", "run")
	in.Variants = domain.MergeVariants(nil, []domain.Variant{
		{ID: "more", PrependArgs: []string{"-v"}, AppendArgs: []string{"--now"}},
		{ID: "swap", Argv: []string{"other", "go"}},
		{ID: "asline", Line: "echo hi"},
	})
	e := fakeEnv("linux", "/usr/bin/tool", "/usr/bin/other", "/bin/sh")
	for variant, want := range map[string][]string{
		"more":   {"/usr/bin/tool", "-v", "run", "--now"},
		"swap":   {"/usr/bin/other", "go"},
		"asline": {"/bin/sh", "-c", "echo hi"},
	} {
		r, err := Resolve(in, variant, e)
		if err != nil {
			t.Fatalf("%s: %v", variant, err)
		}
		if !reflect.DeepEqual(r.Argv, want) {
			t.Errorf("variant %q: %q, want %q", variant, r.Argv, want)
		}
	}
}

func TestVariantSelectionOrderAndErrors(t *testing.T) {
	in := lineInput("sh", "base")
	in.Variants = domain.MergeVariants(
		[]domain.Variant{{ID: "p", Line: "from-profile"}},
		[]domain.Variant{{ID: "i", Line: "from-item"}},
	)
	e := fakeEnv("linux", "/bin/sh")
	pick := func(chosen, item, profile string) string {
		in.ItemDefault, in.ProfileDefault = item, profile
		r, err := Resolve(in, chosen, e)
		if err != nil {
			t.Fatal(err)
		}
		return r.Variant + ":" + r.Argv[2]
	}
	if got := pick("", "", ""); got != "default:base" {
		t.Error(got)
	}
	if got := pick("", "", "p"); got != "p:from-profile" {
		t.Error(got)
	}
	if got := pick("", "i", "p"); got != "i:from-item" {
		t.Error(got)
	}
	if got := pick("p", "i", ""); got != "p:from-profile" {
		t.Error(got)
	}
	in.ItemDefault = ""
	if _, err := Resolve(in, "missing", e); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown variant must be ErrNotFound, got %v", err)
	}
}

func TestPlaceholders(t *testing.T) {
	e := fakeEnv("linux", "/bin/sh")
	in := lineInput("sh", "serve --port {port} --dir {projectDir}")
	in.Cwd = "{projectDir}/app"
	in.Env = map[string]string{"URL": "http://127.0.0.1:{port}/"}
	in.Vars = map[string]string{"port": "6421", "projectDir": "/work/shop"}
	r, err := Resolve(in, "", e)
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[2] != "serve --port 6421 --dir /work/shop" || r.Cwd != "/work/shop/app" {
		t.Fatalf("%q in %q", r.Argv[2], r.Cwd)
	}
	if !contains(r.Env, "URL=http://127.0.0.1:6421/") {
		t.Fatalf("env %q", r.Env)
	}

	in.Vars = map[string]string{"projectDir": "/work/shop"}
	if _, err := Resolve(in, "", e); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "{port}") {
		t.Fatalf("a placeholder without a value must be refused, got %v", err)
	}
}

func TestScriptBlocksAndJSONAreNotPlaceholders(t *testing.T) {
	in := lineInput("pwsh", `Get-Process | ForEach-Object { $_.Name } ; echo '{"a":1}'`)
	r, err := Resolve(in, "", fakeEnv("windows", `C:\ps\pwsh.exe`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Argv[len(r.Argv)-1], "{ $_.Name }") {
		t.Fatalf("script block was changed: %q", r.Argv)
	}
}

func TestWorkingDirectoryDefaults(t *testing.T) {
	e := fakeEnv("linux", "/bin/sh")
	in := lineInput("sh", "x")
	if r, _ := Resolve(in, "", e); r.Cwd != "/home/user" {
		t.Fatalf("no cwd and no project: %q", r.Cwd)
	}
	in.Vars = map[string]string{"projectDir": "/work/p"}
	if r, _ := Resolve(in, "", e); r.Cwd != "/work/p" {
		t.Fatalf("project folder: %q", r.Cwd)
	}
	in.Cwd = "/elsewhere"
	if r, _ := Resolve(in, "", e); r.Cwd != "/elsewhere" {
		t.Fatalf("explicit cwd: %q", r.Cwd)
	}
}

func TestEnvironmentLayering(t *testing.T) {
	in := lineInput("sh", "x")
	in.Env = map[string]string{"A": "item", "NEWVAR": "n"}
	in.EnvFile = ".env"
	in.Cwd = "/proj"
	in.Variants = domain.MergeVariants(nil, []domain.Variant{{ID: "v", Env: map[string]string{"A": "variant"}}})

	e := fakeEnv("linux", "/bin/sh")
	var readPath string
	e.ReadFile = func(p string) ([]byte, error) {
		readPath = p
		return []byte("\xef\xbb\xbf# comment\nA=file\nB=\"quoted value\"\nexport C='single'\n\nExisting=fromfile\n"), nil
	}
	r, err := Resolve(in, "v", e)
	if err != nil {
		t.Fatal(err)
	}
	if readPath != "/proj/.env" && readPath != `\proj\.env` {
		t.Fatalf("env file read from %q", readPath)
	}
	got := map[string]string{}
	for _, kv := range r.Env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{"PATH": "/bin", "Existing": "fromfile", "A": "variant", "B": "quoted value", "C": "single", "NEWVAR": "n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env %v\nwant %v", got, want)
	}
	if len(r.Env) != len(want) {
		t.Fatalf("duplicate names in %q", r.Env)
	}
}

func TestWindowsEnvironmentNamesAreCaseInsensitive(t *testing.T) {
	in := lineInput("cmd", "x")
	in.Env = map[string]string{"PATH": "C:\\new"}
	e := fakeEnv("windows", `C:\win\cmd.exe`)
	e.Environ = []string{"Path=C:\\old", "Other=1"}
	r, err := Resolve(in, "", e)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Env) != 2 || r.Env[0] != `PATH=C:\new` {
		t.Fatalf("env %q", r.Env)
	}
}

func TestBadEnvFileIsRefused(t *testing.T) {
	in := lineInput("sh", "x")
	in.EnvFile = "bad.env"
	e := fakeEnv("linux", "/bin/sh")
	e.ReadFile = func(string) ([]byte, error) { return []byte("not a pair\n"), nil }
	if _, err := Resolve(in, "", e); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	e.ReadFile = func(string) ([]byte, error) { return nil, errors.New("missing") }
	if _, err := Resolve(in, "", e); err == nil {
		t.Fatal("a missing env file must be an error")
	}
}

func TestCopyText(t *testing.T) {
	mk := func(shell, line string, argv ...string) Input {
		in := lineInput(shell, line)
		if argv != nil {
			in = directInput(argv...)
		}
		in.Cwd = "/work/my app"
		in.Env = map[string]string{"TOKEN": "s3cret", "MODE": "it's on"}
		in.SecretEnv = []string{"TOKEN"}
		return in
	}
	win := fakeEnv("windows", `C:\ps\pwsh.exe`, `C:\win\cmd.exe`, `C:\bin\tool.exe`)
	nix := fakeEnv("linux", "/bin/bash", "/usr/bin/tool", "/bin/sh")
	cases := []struct {
		name       string
		in         Input
		env        Env
		full, bare string
	}{
		{"pwsh line", mk("pwsh", "tool --x"), win,
			`Set-Location -LiteralPath '/work/my app'; $env:MODE = 'it''s on'; $env:TOKEN = '<set TOKEN>'; tool --x`, "tool --x"},
		{"cmd line", mk("cmd", "tool --x"), win,
			`cd /d "/work/my app" && set "MODE=it's on" && set "TOKEN=<set TOKEN>" && tool --x`, "tool --x"},
		{"bash line", mk("bash", "tool --x"), nix,
			`cd '/work/my app' && export MODE='it'\''s on' && export TOKEN='<set TOKEN>' && tool --x`, "tool --x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Resolve(c.in, "", c.env)
			if err != nil {
				t.Fatal(err)
			}
			if got := r.CopyText(false); got != c.full {
				t.Errorf("full:\n got %s\nwant %s", got, c.full)
			}
			if got := r.CopyText(true); got != c.bare {
				t.Errorf("bare: got %q, want %q", got, c.bare)
			}
			if strings.Contains(r.CopyText(false), "s3cret") {
				t.Error("a secret value leaked into the copied text")
			}
		})
	}
}

func TestCopyTextForDirectCommandsUsesTheDefaultShellsQuoting(t *testing.T) {
	in := directInput("tool", "a b", "it's", "$x")
	nix := fakeEnv("linux", "/usr/bin/tool", "/bin/sh")
	r, err := Resolve(in, "", nix)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.CopyText(true); got != `tool 'a b' 'it'\''s' '$x'` {
		t.Fatalf("posix: %s", got)
	}
	win := fakeEnv("windows", `C:\Program Files\t\tool.exe`, `C:\ps\pwsh.exe`)
	r, err = Resolve(directInput("tool", "a b"), "", win)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.CopyText(true); got != "tool 'a b'" {
		t.Fatalf("powershell: %s", got)
	}
}

func TestCopyTextOfAnInteractiveShell(t *testing.T) {
	r, err := Resolve(lineInput("bash", ""), "", fakeEnv("linux", "/bin/bash"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.CopyText(true); got != "bash" {
		t.Fatalf("got %q", got)
	}
}

func TestCopiedCommandIsWhatRuns(t *testing.T) {
	// The line the shell executes is the text Copy shows.
	in := lineInput("bash", "backlog browser --port {port}")
	in.Vars = map[string]string{"port": "6421"}
	in.Variants = domain.MergeVariants(nil, []domain.Variant{{ID: "q", AppendArgs: []string{"--no-open"}}})
	r, err := Resolve(in, "q", fakeEnv("linux", "/bin/bash"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[len(r.Argv)-1] != r.CopyText(true) {
		t.Fatalf("run %q != copy %q", r.Argv[len(r.Argv)-1], r.CopyText(true))
	}
}

func TestTheResolverIgnoresTheItemKind(t *testing.T) {
	cmd := domain.Command{Shell: "bash", Line: "tool --go", Env: map[string]string{"K": "v"}, Cwd: "/w"}
	e := fakeEnv("linux", "/bin/bash")
	var ref Resolved
	for i, kind := range []domain.Kind{domain.KindService, domain.KindTask, domain.KindShell, domain.KindAgent} {
		item := domain.Item{ID: "i_0123456789", Alias: "x", Kind: kind, Command: cmd}
		r, err := Resolve(FromItem(item, nil, nil), "", e)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if i == 0 {
			ref = r
			continue
		}
		if !reflect.DeepEqual(r.Argv, ref.Argv) || r.Cwd != ref.Cwd || r.CopyText(false) != ref.CopyText(false) {
			t.Fatalf("kind %s resolved differently", kind)
		}
	}
}

func TestFromItemProvidesItemAndProjectPlaceholders(t *testing.T) {
	item := domain.Item{ID: "i_0123456789", Alias: "shop-backlog", Kind: domain.KindService,
		Command: domain.Command{Shell: "sh", Line: "echo {alias} {projectName} {itemId} {port}", Cwd: "{projectDir}"}}
	project := &domain.Project{ID: "p_0123456789", Name: "Shop", Path: "/work/shop"}
	r, err := Resolve(FromItem(item, project, map[string]string{"port": "6421"}), "", fakeEnv("linux", "/bin/sh"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[2] != "echo shop-backlog Shop i_0123456789 6421" || r.Cwd != "/work/shop" {
		t.Fatalf("%q %q", r.Argv[2], r.Cwd)
	}
}

func TestSecretEnvNamesAreValidated(t *testing.T) {
	item := domain.Item{ID: "i_0123456789", Alias: "x", Kind: domain.KindTask,
		Command: domain.Command{Shell: "sh", Line: "x", SecretEnv: []string{"GOOD", "bad name"}}}
	err := item.Validate()
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(fmt.Sprint(err), "secretEnv[1]") {
		t.Fatalf("got %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
