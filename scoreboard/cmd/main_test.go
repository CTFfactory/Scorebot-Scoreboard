package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
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
