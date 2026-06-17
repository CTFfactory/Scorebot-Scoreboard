package scoreboard

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

func TestConfigHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CONFIG_HELPER") != "1" {
		return
	}
	switch os.Getenv("CONFIG_HELPER_MODE") {
	case "usage":
		args, _, _, _ := newFlags(&config{})
		args.Usage()
	case "cmdline-help":
		os.Args = []string{"scoreboard", "-h"}
		if _, err := Cmdline(); err == flag.ErrHelp {
			os.Exit(2)
		} else if err != nil {
			os.Exit(1)
		}
	}
	os.Exit(0)
}

func runConfigHelper(t *testing.T, mode string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestConfigHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_CONFIG_HELPER=1", "CONFIG_HELPER_MODE="+mode)
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
	t.Fatalf("run config helper failed: %v", err)
	return 0, "", ""
}

func TestConfigVerifyDefaults(t *testing.T) {
	c := config{}
	if err := c.verify(); err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
	if c.Tick != 5 {
		t.Fatalf("expected default tick 5, got %d", c.Tick)
	}
	if c.Timeout != 10 {
		t.Fatalf("expected default timeout 10, got %d", c.Timeout)
	}
	if c.Listen != "0.0.0.0:8080" {
		t.Fatalf("expected default listen, got %q", c.Listen)
	}
}

func TestNewFlagsUsagePath(t *testing.T) {
	code, out, _ := runConfigHelper(t, "usage")
	if code != 0 {
		t.Fatalf("expected usage helper to exit with code 0, got %d", code)
	}
	if !strings.Contains(out, "Usage of scoreboard:") {
		t.Fatalf("expected usage output, got %q", out)
	}
}

func TestCmdlineHelpFlagPath(t *testing.T) {
	code, out, errOut := runConfigHelper(t, "cmdline-help")
	if code != 0 && code != 2 {
		t.Fatalf("expected help helper exit code 0 or 2, got %d (%s)", code, errOut)
	}
	if !strings.Contains(out, "Usage of scoreboard:") {
		t.Fatalf("expected help usage output, got %q", out)
	}
	if strings.Count(out, "Usage of scoreboard:") != 1 {
		t.Fatalf("expected help usage to be printed once, got %q", out)
	}
}

func TestCmdlineVersionAndDefaults(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"scoreboard", "-V"}
	out := captureStdout(t, func() {
		s, err := Cmdline()
		if err != nil {
			t.Fatalf("Cmdline -V error: %v", err)
		}
		if s != nil {
			t.Fatalf("expected nil scoreboard for -V")
		}
	})
	if !strings.Contains(out, "Scorebot Scoreboard:") {
		t.Fatalf("expected version output, got %q", out)
	}

	os.Args = []string{"scoreboard", "-d"}
	out = captureStdout(t, func() {
		s, err := Cmdline()
		if err != nil {
			t.Fatalf("Cmdline -d error: %v", err)
		}
		if s != nil {
			t.Fatalf("expected nil scoreboard for -d")
		}
	})
	if !strings.Contains(out, "\"scorebot\"") {
		t.Fatalf("expected defaults JSON, got %q", out)
	}
}

func TestCmdlineMissingRequiredArgs(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"scoreboard"}
	out := captureStdout(t, func() {
		s, err := Cmdline()
		if err != flag.ErrHelp {
			t.Fatalf("expected flag.ErrHelp, got %v", err)
		}
		if s != nil {
			t.Fatalf("expected nil scoreboard")
		}
	})
	if !strings.Contains(out, "Usage of scoreboard:") {
		t.Fatalf("expected usage output, got %q", out)
	}
}

func TestCmdlineInvalidFlagReturnsHelp(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"scoreboard", "-not-a-real-flag"}
	out := captureStdout(t, func() {
		s, err := Cmdline()
		if err != flag.ErrHelp {
			t.Fatalf("expected flag.ErrHelp for invalid flag, got %v", err)
		}
		if s != nil {
			t.Fatalf("expected nil scoreboard for invalid flag")
		}
	})
	if !strings.Contains(out, "Usage of scoreboard:") {
		t.Fatalf("expected usage output, got %q", out)
	}
}

func TestCmdlineConfigFile(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "scoreboard.json")
	cfg := `{"scorebot":"http://example","tick":1,"timeout":1,"listen":"127.0.0.1:0"}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	os.Args = []string{"scoreboard", "-c", cfgPath}
	s, err := Cmdline()
	if err != nil {
		t.Fatalf("Cmdline config error: %v", err)
	}
	if s == nil {
		t.Fatalf("expected scoreboard instance")
	}
	if got := s.Addr; got != "127.0.0.1:0" {
		t.Fatalf("expected server address 127.0.0.1:0, got %q", got)
	}
}

func TestCmdlineInvalidConfigJSON(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(cfgPath, []byte(`{"scorebot":`), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	os.Args = []string{"scoreboard", "-c", cfgPath}
	if _, err := Cmdline(); err == nil {
		t.Fatalf("expected JSON parse error for invalid config")
	}
}

func TestCmdlineMissingConfigFile(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"scoreboard", "-c", filepath.Join(t.TempDir(), "missing.json")}
	if _, err := Cmdline(); err == nil {
		t.Fatalf("expected read error for missing config file")
	}
}

func TestCmdlineConfigVerifyAfterFileLoad(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "scoreboard-zeroes.json")
	cfg := `{"scorebot":"http://example","tick":0,"timeout":0,"listen":""}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	os.Args = []string{"scoreboard", "-c", cfgPath}
	s, err := Cmdline()
	if err != nil {
		t.Fatalf("Cmdline config error: %v", err)
	}
	if s == nil {
		t.Fatalf("expected scoreboard instance")
	}
	if s.Addr != "0.0.0.0:8080" {
		t.Fatalf("expected verify() to restore default listen, got %q", s.Addr)
	}
	if s.ReadTimeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %v", s.ReadTimeout)
	}
}

func TestParseFlagsDirect(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	os.Args = []string{"scoreboard", "-not-a-real-flag"}
	args := flag.NewFlagSet("Scorebot Scoreboard", flag.ContinueOnError)
	args.SetOutput(io.Discard)
	out := captureStdout(t, func() {
		if err := parseFlags(args); err != flag.ErrHelp {
			t.Fatalf("expected flag.ErrHelp, got %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no stdout output for raw flag set, got %q", out)
	}

	os.Args = []string{"scoreboard", "-h"}
	args, _, _, _ = newFlags(&config{})
	out = captureStdout(t, func() {
		if err := parseFlags(args); err != flag.ErrHelp {
			t.Fatalf("expected flag.ErrHelp for -h, got %v", err)
		}
	})
	if strings.Count(out, "Usage of scoreboard:") != 1 {
		t.Fatalf("expected single usage output for -h, got %q", out)
	}

	os.Args = []string{"scoreboard"}
	args = flag.NewFlagSet("Scorebot Scoreboard", flag.ContinueOnError)
	if err := parseFlags(args); err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
}

func TestLoadConfigFileDirect(t *testing.T) {
	var c config
	if err := loadConfigFile(&c, ""); err != nil {
		t.Fatalf("expected empty file path to be a no-op, got %v", err)
	}

	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"scorebot":"http://example"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := loadConfigFile(&c, good); err != nil {
		t.Fatalf("loadConfigFile success path: %v", err)
	}
	if c.Scorebot != "http://example" {
		t.Fatalf("expected scorebot field to be loaded, got %q", c.Scorebot)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"scorebot":`), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	if err := loadConfigFile(&c, bad); err == nil {
		t.Fatalf("expected decode error for invalid config json")
	}
}
