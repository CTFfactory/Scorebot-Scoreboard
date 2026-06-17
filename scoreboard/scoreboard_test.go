package scoreboard

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/CTFfactory/Scorebot-Scoreboard/scoreboard/game"
	"github.com/gorilla/websocket"
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

func TestGetTemplateFallsBackWhenOverrideMissing(t *testing.T) {
	dir := t.TempDir()
	tmpl := template.New("base")
	if err := getTemplate(tmpl, dir, "scoreboard.html"); err != nil {
		t.Fatalf("expected embedded fallback when override file missing: %v", err)
	}
	if tmpl.Lookup("scoreboard.html") == nil {
		t.Fatalf("expected fallback template lookup success")
	}
}

func TestGetTemplateOverrideParseError(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "home.html")
	if err := os.WriteFile(override, []byte("{{ if }}"), 0o600); err != nil {
		t.Fatalf("write bad template: %v", err)
	}
	tmpl := template.New("base")
	if err := getTemplate(tmpl, dir, "home.html"); err == nil {
		t.Fatalf("expected parse error from invalid override template")
	}
}

func TestGetTemplateMissingEmbeddedFile(t *testing.T) {
	tmpl := template.New("base")
	if err := getTemplate(tmpl, "", "missing-template.html"); err == nil {
		t.Fatalf("expected missing embedded template error")
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

func TestConfigNewDirectoryOverrideSuccess(t *testing.T) {
	dir := t.TempDir()
	publicDir := filepath.Join(dir, "public")
	templateDir := filepath.Join(dir, "template")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("mkdir template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "asset.txt"), []byte("asset"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "home.html"), []byte("HOME-OVERRIDE"), 0o600); err != nil {
		t.Fatalf("write home template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "scoreboard.html"), []byte("SCORE-OVERRIDE {{.Game}}"), 0o600); err != nil {
		t.Fatalf("write scoreboard template: %v", err)
	}

	s, err := (config{
		Directory: dir,
		Scorebot:  "http://example",
		Listen:    "127.0.0.1:0",
		Tick:      1,
		Timeout:   1,
	}).New()
	if err != nil {
		t.Fatalf("config.New with directory override: %v", err)
	}
	f, err := s.Open("asset.txt")
	if err != nil {
		t.Fatalf("open local override asset: %v", err)
	}
	_ = f.Close()
}

func TestConfigNewDirectoryTemplateParseError(t *testing.T) {
	dir := t.TempDir()
	publicDir := filepath.Join(dir, "public")
	templateDir := filepath.Join(dir, "template")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("mkdir template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "home.html"), []byte("{{ if }}"), 0o600); err != nil {
		t.Fatalf("write bad template: %v", err)
	}
	_, err := (config{
		Directory: dir,
		Scorebot:  "http://example",
		Listen:    "127.0.0.1:0",
		Tick:      1,
		Timeout:   1,
	}).New()
	if err == nil {
		t.Fatalf("expected template parse error")
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

func TestScoreboardHTTPHandler(t *testing.T) {
	tmpl := template.New("base")
	template.Must(tmpl.New("home.html").Parse("HOME"))
	template.Must(tmpl.New("scoreboard.html").Parse("SCORE {{.Game}}"))

	s := &Scoreboard{
		Manager: &game.Manager{},
		html:    tmpl,
		fs: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Static", "1")
			_, _ = w.Write([]byte("STATIC"))
		}),
	}

	t.Run("reject non-get", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})

	t.Run("render home template", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if body := w.Body.String(); body != "HOME" {
			t.Fatalf("expected home template body, got %q", body)
		}
		if cors := w.Header().Get("Access-Control-Allow-Origin"); cors != "*" {
			t.Fatalf("expected wildcard CORS header, got %q", cors)
		}
	})

	t.Run("render scoreboard template by game id path", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/game/123", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if body := w.Body.String(); body != "SCORE 123" {
			t.Fatalf("expected scoreboard template body, got %q", body)
		}
	})

	t.Run("fallback to static handler", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/game/not-a-number", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if body := w.Body.String(); body != "STATIC" {
			t.Fatalf("expected static handler body, got %q", body)
		}
		if header := w.Header().Get("X-Static"); header != "1" {
			t.Fatalf("expected static handler header, got %q", header)
		}
	})

	t.Run("fallback to static handler for slug path", func(t *testing.T) {
		s.Manager = &game.Manager{}
		r := httptest.NewRequest(http.MethodGet, "/slugpath", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if body := w.Body.String(); body != "STATIC" {
			t.Fatalf("expected static handler body, got %q", body)
		}
	})

	t.Run("fallback when trimmed path is empty", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "///", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if body := w.Body.String(); body != "STATIC" {
			t.Fatalf("expected static handler body, got %q", body)
		}
	})

	t.Run("fallback when path prefix is not game", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/abc/123", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if body := w.Body.String(); body != "STATIC" {
			t.Fatalf("expected static handler body, got %q", body)
		}
	})
}

func TestScoreboardHTTPTemplateExecutionErrors(t *testing.T) {
	s := &Scoreboard{
		Manager: &game.Manager{},
		fs: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("STATIC"))
		}),
	}
	t.Run("home template missing", func(t *testing.T) {
		s.html = template.Must(template.New("base").New("scoreboard.html").Parse("SCORE {{.Game}}"))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 when home template missing, got %d", w.Code)
		}
	})

	t.Run("scoreboard template missing", func(t *testing.T) {
		s.html = template.Must(template.New("base").New("home.html").Parse("HOME"))
		r := httptest.NewRequest(http.MethodGet, "/game/1", nil)
		w := httptest.NewRecorder()
		s.http(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 when scoreboard template missing, got %d", w.Code)
		}
	})
}

func TestScoreboardListenWithoutTLS(t *testing.T) {
	s := &Scoreboard{
		Server: &http.Server{
			Addr:    "invalid-listen-address",
			Handler: http.NewServeMux(),
		},
	}
	called := false
	var err error
	var lock sync.Mutex
	s.listen(&err, &lock, func() { called = true })
	if err == nil {
		t.Fatalf("expected listen error for invalid address")
	}
	if !called {
		t.Fatalf("expected cancel callback to be invoked")
	}
}

func TestScoreboardListenWithTLSFilesError(t *testing.T) {
	s := &Scoreboard{
		Server: &http.Server{
			Addr:    "127.0.0.1:0",
			Handler: http.NewServeMux(),
		},
		cert: "/path/that/does/not/exist-cert.pem",
		key:  "/path/that/does/not/exist-key.pem",
	}
	called := false
	var err error
	var lock sync.Mutex
	s.listen(&err, &lock, func() { called = true })
	if err == nil {
		t.Fatalf("expected TLS listen error when cert/key files are missing")
	}
	if !called {
		t.Fatalf("expected cancel callback to be invoked")
	}
}

func TestScoreboardHTTPWebsocketUpgradeFailure(t *testing.T) {
	s := &Scoreboard{
		ws: &websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/w", nil)
	w := httptest.NewRecorder()
	s.httpWebsocket(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid websocket upgrade, got %d", w.Code)
	}
}

func TestScoreboardHTTPWebsocketSuccessPath(t *testing.T) {
	m, err := game.New("http://127.0.0.1:1", "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	s := &Scoreboard{
		Manager: m,
		ws: &websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}

	server := httptest.NewServer(http.HandlerFunc(s.httpWebsocket))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte(`{"bad":"hello"}`)); err != nil {
		t.Fatalf("write invalid hello payload: %v", err)
	}
}

func TestConfigNewDirectoryNotFound(t *testing.T) {
	_, err := (config{
		Directory: filepath.Join(t.TempDir(), "missing"),
		Scorebot:  "http://example",
		Listen:    "127.0.0.1:0",
		Tick:      1,
		Timeout:   1,
	}).New()
	if err == nil {
		t.Fatalf("expected error for missing directory")
	}
}

func TestScoreboardRunWhenListenFails(t *testing.T) {
	m, err := game.New("http://127.0.0.1:1", "", time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	s := &Scoreboard{
		Manager: m,
		Server: &http.Server{
			Addr:        "invalid-listen-address",
			Handler:     http.NewServeMux(),
			ReadTimeout: 20 * time.Millisecond,
		},
	}
	if err := s.Run(); err != nil {
		t.Fatalf("expected nil shutdown error on listen failure path, got %v", err)
	}
}
