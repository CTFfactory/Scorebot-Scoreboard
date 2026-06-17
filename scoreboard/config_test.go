package scoreboard

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
