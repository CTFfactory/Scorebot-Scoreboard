// Copyright(C) 2020 - 2026 iDigitalFlame
// Copyright(C) 2026 luftegrof
//
// This program is free software: you can redistribute it and / or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.If not, see <https://www.gnu.org/licenses/>.

package scoreboard

import (
	"context"
	"crypto/tls"
	"embed"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/CTFfactory/Scorebot-Scoreboard/scoreboard/game"
	"github.com/gorilla/websocket"
)

//go:embed html
var resources embed.FS

type display struct {
	Game    uint64
	Twitter bool
}

type Scoreboard struct {
	fs http.Handler
	*game.Manager
	*http.Server
	ws     *websocket.Upgrader
	html   *template.Template
	key    string
	cert   string
	dir    http.FileSystem
	expire time.Duration
}

func configDirectoryPaths(directory string) (templateDir, publicDir string, err error) {
	if len(directory) == 0 {
		return "", "", nil
	}
	publicDir = filepath.Join(directory, "public")
	d, err := os.Stat(publicDir)
	if err != nil {
		return "", "", err
	}
	if !d.IsDir() {
		return "", "", fs.ErrInvalid
	}
	return filepath.Join(directory, "template"), publicDir, nil
}

func loadCoreTemplates(t *template.Template, directory string) error {
	for _, name := range []string{"home.html", "scoreboard.html"} {
		if err := getTemplate(t, directory, name); err != nil {
			return err
		}
	}
	return nil
}

func newHTTPServer(addr string, timeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           new(http.ServeMux),
		ReadTimeout:       timeout,
		IdleTimeout:       timeout,
		WriteTimeout:      timeout,
		ReadHeaderTimeout: timeout,
	}
}

func newWebSocketUpgrader(timeout time.Duration) *websocket.Upgrader {
	return &websocket.Upgrader{
		CheckOrigin:      checkWebSocketOrigin,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
		HandshakeTimeout: timeout,
	}
}

func (s *Scoreboard) Run() error {
	var (
		err  error
		w    = make(chan os.Signal, 1)
		x, c = context.WithCancel(context.Background())
		l    sync.Mutex
	)
	s.BaseContext = func(_ net.Listener) context.Context { return x }
	signal.Notify(w, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	slog.Info("Starting Scoreboard service..")
	go s.listen(&err, &l, c)
	go s.Start(x)
	select {
	case <-w:
	case <-x.Done():
	}
	signal.Stop(w)
	close(w)
	c()
	l.Lock()
	runtimeErr := err
	l.Unlock()
	if runtimeErr != nil {
		slog.Error("Received error during runtime", "error", runtimeErr.Error())
	}
	slog.Info("Stopping and shutting down..")
	f, u := context.WithTimeout(x, s.ReadTimeout)
	err = s.Shutdown(f)
	s.Close()
	u()
	return err
}

func (c config) New() (*Scoreboard, error) {
	timeout := time.Second * time.Duration(c.Timeout)
	templateDir, publicDir, err := configDirectoryPaths(c.Directory)
	if err != nil {
		return nil, err
	}
	var s Scoreboard
	s.html = template.New("base")
	if err := loadCoreTemplates(s.html, templateDir); err != nil {
		return nil, err
	}
	s.Manager, err = game.New(c.Scorebot, c.Assets, time.Duration(c.Tick)*time.Second, timeout)
	if err != nil {
		return nil, err
	}
	s.Server = newHTTPServer(c.Listen, timeout)
	s.ws = newWebSocketUpgrader(timeout)
	s.key, s.cert = c.Key, c.Cert
	s.fs, s.dir = http.FileServer(http.FS(&s)), http.Dir(publicDir)
	s.Server.Handler.(*http.ServeMux).HandleFunc("/", s.http)
	s.Server.Handler.(*http.ServeMux).HandleFunc("/w", s.httpWebsocket)
	return &s, nil
}

func (s *Scoreboard) Open(n string) (fs.File, error) {
	f, err := s.dir.Open(n)
	if err == nil {
		return f, nil
	}
	r, err := resources.Open("html/public/" + n)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func getTemplate(t *template.Template, d, f string) error {
	parsed, err := parseOverrideTemplate(t, d, f)
	if err != nil {
		return err
	}
	if parsed {
		return nil
	}
	return parseEmbeddedTemplate(t, f)
}

func parseOverrideTemplate(t *template.Template, d, f string) (bool, error) {
	if len(d) == 0 {
		return false, nil
	}
	s := filepath.Join(d, f)
	i, err := os.Stat(s)
	if err != nil || i.IsDir() {
		return false, nil
	}
	if _, err := t.New(f).ParseFiles(s); err != nil {
		return false, err
	}
	return true, nil
}

func parseEmbeddedTemplate(t *template.Template, f string) error {
	b, err := resources.ReadFile("html/template/" + f)
	if err != nil {
		return err
	}
	if _, err := t.New(f).Parse(string(b)); err != nil {
		return err
	}
	return nil
}

func checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if len(origin) == 0 {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || len(u.Host) == 0 {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Scoreboard) listen(err *error, l *sync.Mutex, f context.CancelFunc) {
	if len(s.cert) == 0 || len(s.key) == 0 {
		l.Lock()
		*err = s.ListenAndServe()
		l.Unlock()
		f()
		return
	}
	s.TLSConfig = &tls.Config{
		NextProtos: []string{"h2", "http/1.1"},
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.X25519},
	}
	l.Lock()
	*err = s.ListenAndServeTLS(s.cert, s.key)
	l.Unlock()
	f()
}

func (s *Scoreboard) http(w http.ResponseWriter, r *http.Request) {
	if !isGet(r.Method) {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if isHomePath(r.URL.Path) {
		s.renderHome(w)
		return
	}
	v, ok := s.resolveGameID(r.URL.Path)
	if !ok {
		s.fs.ServeHTTP(w, r)
		return
	}
	s.renderScoreboard(w, r, v)
}

func isGet(method string) bool {
	return method == http.MethodGet
}

func isHomePath(path string) bool {
	return len(path) <= 1 || path == "/"
}

func (s *Scoreboard) renderHome(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.html.ExecuteTemplate(w, "home.html", s.Games); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		slog.Error("Error during home template execution", "error", err.Error())
	}
}

func (s *Scoreboard) resolveGameID(path string) (uint64, bool) {
	n := strings.Trim(path, "/")
	if len(n) == 0 {
		return 0, false
	}
	i := strings.IndexRune(n, '/')
	if i < 0 {
		v := s.Game(n)
		return v, v > 0
	}
	if strings.ToLower(n[:i]) != "game" {
		return 0, false
	}
	x, err := strconv.ParseUint(n[i+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return x, x > 0
}

func (s *Scoreboard) renderScoreboard(w http.ResponseWriter, r *http.Request, gameID uint64) {
	slog.Debug("Received scoreboard request", "remote", r.RemoteAddr)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.html.ExecuteTemplate(w, "scoreboard.html", &display{Game: gameID, Twitter: false}); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		slog.Error("Error during scoreboard template execution", "remote", r.RemoteAddr, "error", err.Error())
	}
}

func (s *Scoreboard) httpWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := s.ws.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	s.New(c)
}
