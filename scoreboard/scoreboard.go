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
	var (
		t    = time.Second * time.Duration(c.Timeout)
		err  error
		x, p string
	)
	if len(c.Directory) > 0 {
		p = filepath.Join(c.Directory, "public")
		var d fs.FileInfo
		if d, err = os.Stat(p); err != nil {
			return nil, err
		}
		if !d.IsDir() {
			return nil, fs.ErrInvalid
		}
		x = filepath.Join(c.Directory, "template")
	}
	var s Scoreboard
	s.html = template.New("base")
	if err = getTemplate(s.html, x, "home.html"); err != nil {
		return nil, err
	}
	if err = getTemplate(s.html, x, "scoreboard.html"); err != nil {
		return nil, err
	}
	if s.Manager, err = game.New(c.Scorebot, c.Assets, time.Duration(c.Tick)*time.Second, t); err != nil {
		return nil, err
	}
	s.Server = &http.Server{
		Addr:              c.Listen,
		Handler:           new(http.ServeMux),
		ReadTimeout:       t,
		IdleTimeout:       t,
		WriteTimeout:      t,
		ReadHeaderTimeout: t,
	}
	s.ws = &websocket.Upgrader{
		CheckOrigin:      checkWebSocketOrigin,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
		HandshakeTimeout: t,
	}
	s.key, s.cert = c.Key, c.Cert
	s.fs, s.dir = http.FileServer(http.FS(&s)), http.Dir(p)
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
	if len(d) > 0 {
		s := filepath.Join(d, f)
		if i, err := os.Stat(s); err == nil && !i.IsDir() {
			if _, err = t.New(f).ParseFiles(s); err != nil {
				return err
			}
			return nil
		}
	}
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
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if len(r.URL.Path) <= 1 || r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.html.ExecuteTemplate(w, "home.html", s.Games); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			slog.Error("Error during home template execution", "error", err.Error())
		}
		return
	}
	var (
		v uint64
		n = strings.Trim(r.URL.Path, "/")
		i = strings.IndexRune(n, '/')
	)
	if len(n) == 0 {
		s.fs.ServeHTTP(w, r)
		return
	}
	switch {
	case i < 0:
		v = s.Game(n)
	case strings.ToLower(n[:i]) == "game":
		if x, err := strconv.ParseUint(n[i+1:], 10, 64); err == nil {
			v = x
		}
	}
	if v == 0 {
		s.fs.ServeHTTP(w, r)
		return
	}
	slog.Debug("Received scoreboard request", "remote", r.RemoteAddr)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.html.ExecuteTemplate(w, "scoreboard.html", &display{Game: v, Twitter: false}); err != nil {
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
