package main

import (
	"bytes"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/CTFfactory/Scorebot-Scoreboard/scoreboard"
	"github.com/CTFfactory/Scorebot-Scoreboard/scoreboard/game"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i := range os.Args {
		if os.Args[i] == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	os.Args = append([]string{"scoreboard"}, args...)
	main()
}

func runMain(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestHelperProcess", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	t.Fatalf("run helper process failed: %v", err)
	return 0, "", ""
}

func TestMainVersionFlag(t *testing.T) {
	code, out, errOut := runMain(t, "-V")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (%s)", code, errOut)
	}
	if !strings.Contains(out, "Scorebot Scoreboard:") {
		t.Fatalf("expected version output, got %q", out)
	}
}

func TestMainDefaultsFlag(t *testing.T) {
	code, out, errOut := runMain(t, "-d")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (%s)", code, errOut)
	}
	if !strings.Contains(out, "\"scorebot\"") {
		t.Fatalf("expected defaults JSON output, got %q", out)
	}
}

func TestMainMissingRequiredArgs(t *testing.T) {
	code, out, _ := runMain(t)
	if code != 2 {
		t.Fatalf("expected exit code 2 for help path, got %d", code)
	}
	if !strings.Contains(out, "Usage of scoreboard:") {
		t.Fatalf("expected usage output, got %q", out)
	}
}

func TestMainInvalidFlag(t *testing.T) {
	code, _, errOut := runMain(t, "-invalid-flag")
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid flag, got %d", code)
	}
	if !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("expected invalid-flag error output, got %q", errOut)
	}
}

func TestMainStartupErrorPath(t *testing.T) {
	code, _, errOut := runMain(t, "-c", "/path/that/does/not/exist.json")
	if code != 1 {
		t.Fatalf("expected exit code 1 for startup error path, got %d", code)
	}
	if !strings.Contains(errOut, "Error during startup:") {
		t.Fatalf("expected startup error output, got %q", errOut)
	}
}

func TestRunRuntimeErrorPath(t *testing.T) {
	oldCmdline, oldRun, oldStderr := cmdlineFunc, runFunc, stderrFunc
	t.Cleanup(func() {
		cmdlineFunc = oldCmdline
		runFunc = oldRun
		stderrFunc = oldStderr
	})

	cmdlineFunc = func() (*scoreboard.Scoreboard, error) { return &scoreboard.Scoreboard{}, nil }
	runFunc = func(*scoreboard.Scoreboard) error { return errors.New("runtime boom") }

	var out strings.Builder
	stderrFunc = func(msg string) { _, _ = out.WriteString(msg) }

	if code := run(); code != 1 {
		t.Fatalf("expected exit code 1 for runtime error path, got %d", code)
	}
	if got := out.String(); !strings.Contains(got, "Error during runtime: runtime boom!") {
		t.Fatalf("expected runtime error output, got %q", got)
	}
}

func TestRunSuccessAndHelpPaths(t *testing.T) {
	oldCmdline, oldRun, oldStderr := cmdlineFunc, runFunc, stderrFunc
	t.Cleanup(func() {
		cmdlineFunc = oldCmdline
		runFunc = oldRun
		stderrFunc = oldStderr
	})

	cmdlineFunc = func() (*scoreboard.Scoreboard, error) { return nil, flag.ErrHelp }
	if code := run(); code != 2 {
		t.Fatalf("expected exit code 2 for help path, got %d", code)
	}

	cmdlineFunc = func() (*scoreboard.Scoreboard, error) { return &scoreboard.Scoreboard{}, nil }
	runFunc = func(*scoreboard.Scoreboard) error { return nil }
	stderrFunc = func(string) {}
	if code := run(); code != 0 {
		t.Fatalf("expected exit code 0 for successful run path, got %d", code)
	}
}

func TestDefaultRunFuncPath(t *testing.T) {
	m, err := game.New("http://127.0.0.1:1", "", time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	s := &scoreboard.Scoreboard{
		Manager: m,
		Server: &http.Server{
			Addr:        "invalid-listen-address",
			Handler:     http.NewServeMux(),
			ReadTimeout: 20 * time.Millisecond,
		},
	}
	if err := runFunc(s); err != nil {
		t.Fatalf("expected runFunc default path to return nil, got %v", err)
	}
}
