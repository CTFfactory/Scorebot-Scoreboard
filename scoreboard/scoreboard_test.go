package scoreboard

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestGetTemplateEmbedded(t *testing.T) {
	tmpl := template.New("base")
	if err := getTemplate(tmpl, "", "home.html"); err != nil {
		t.Fatalf("getTemplate embedded: %v", err)
	}
	if tmpl.Lookup("home.html") == nil {
		t.Fatalf("expected embedded template to be loaded")
	}
}

func TestGetTemplateOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "home.html")
	if err := os.WriteFile(override, []byte("override {{.}}"), 0o600); err != nil {
		t.Fatalf("write override template: %v", err)
	}

	tmpl := template.New("base")
	if err := getTemplate(tmpl, dir, "home.html"); err != nil {
		t.Fatalf("getTemplate override: %v", err)
	}
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, "home.html", "ok"); err != nil {
		t.Fatalf("execute override template: %v", err)
	}
	if got := b.String(); got != "override ok" {
		t.Fatalf("expected override template content, got %q", got)
	}
}

func TestScoreboardOpenLocalThenEmbedded(t *testing.T) {
	root := t.TempDir()
	public := filepath.Join(root, "public")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	local := filepath.Join(public, "local.txt")
	if err := os.WriteFile(local, []byte("local-file"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	s := Scoreboard{dir: http.Dir(public)}
	f, err := s.Open("local.txt")
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read local file: %v", err)
	}
	if got := string(content); got != "local-file" {
		t.Fatalf("expected local content, got %q", got)
	}

	embedded, err := s.Open("image/team.png")
	if err != nil {
		t.Fatalf("open embedded fallback: %v", err)
	}
	_ = embedded.Close()
}

func TestConfigNewInvalidPublicPath(t *testing.T) {
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "public")
	if err := os.WriteFile(publicPath, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatalf("write invalid public path: %v", err)
	}

	_, err := (config{
		Directory: dir,
		Scorebot:  "http://example",
		Listen:    "127.0.0.1:0",
		Tick:      1,
		Timeout:   1,
	}).New()
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid, got %v", err)
	}
}

func TestConfigNewSuccess(t *testing.T) {
	s, err := (config{
		Scorebot: "http://example",
		Listen:   "127.0.0.1:0",
		Tick:     1,
		Timeout:  1,
	}).New()
	if err != nil {
		t.Fatalf("config.New error: %v", err)
	}
	if s == nil || s.Server == nil || s.Manager == nil || s.ws == nil {
		t.Fatalf("expected initialized scoreboard with server manager and websocket upgrader")
	}
}

func TestCheckWebSocketOrigin(t *testing.T) {
	req := &http.Request{
		Host:   "example.com",
		Header: make(http.Header),
	}
	if !checkWebSocketOrigin(req) {
		t.Fatalf("expected empty origin to be allowed")
	}

	req.Header.Set("Origin", "https://example.com")
	if !checkWebSocketOrigin(req) {
		t.Fatalf("expected same-host origin to be allowed")
	}

	req.Header.Set("Origin", "https://other.example.com")
	if checkWebSocketOrigin(req) {
		t.Fatalf("expected mismatched host origin to be rejected")
	}

	req.Header.Set("Origin", "://bad-origin")
	if checkWebSocketOrigin(req) {
		t.Fatalf("expected invalid origin to be rejected")
	}
}
