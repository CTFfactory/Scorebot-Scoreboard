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

package game

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var errMissingGame = errors.New("game ID is missing from JSON data")

type hello uint64
type stream struct {
	*websocket.Conn
	ok bool
}

type Manager struct {
	active  map[string]uint64
	tick    *time.Ticker
	subs    map[uint64]*subscription
	client  *http.Client
	url     url.URL
	assets  string
	Games   []meta
	timeout time.Duration
	running uint32
}

type subscription struct {
	new     chan *websocket.Conn
	cache   []update
	clients []*stream
	last    game
	ID      uint64
	stale   uint32
}

func (m *Manager) close() {
	for n, s := range m.subs {
		for i := range s.clients {
			s.clients[i].Close()
			s.clients[i] = nil
		}
		close(s.new)
		delete(m.subs, n)
	}
	m.tick.Stop()
}

func cleanSlugString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range s {
		switch {
		case s[i] <= '9' && s[i] >= '0':
			b.WriteByte(s[i])
		case s[i] <= 'Z' && s[i] >= 'A':
			b.WriteByte(s[i])
		case s[i] <= 'z' && s[i] >= 'a':
			b.WriteByte(s[i])
		case s[i] == '-' || s[i] == '.' || s[i] == '_':
			b.WriteByte(s[i])
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (m *Manager) Game(s string) uint64 {
	return m.active[strings.ToLower(cleanSlugString(s))]
}

func (m *Manager) New(n *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Collection newclient function recovered from a panic", "error", err)
		}
	}()
	slog.Debug("Received connection, listening for Hello", "remote", n.RemoteAddr().String())
	var h hello
	if err := n.ReadJSON(&h); err != nil {
		slog.Error("Could not read Hello message", "remote", n.RemoteAddr().String(), "error", err.Error())
		n.Close()
		return
	}
	slog.Debug("Received Hello with requested Game ID", "id", h, "remote", n.RemoteAddr().String())
	s, ok := m.subs[uint64(h)]
	if !ok || s == nil {
		slog.Debug("Checking Game ID requested by remote", "id", h, "remote", n.RemoteAddr().String())
		var g game
		// FastAPI core endpoint maps to /api/games/{id}/scoreboard
		if err := m.getJSON(context.Background(), "api/games/"+strconv.FormatUint(uint64(h), 10)+"/scoreboard", &g); err != nil {
			slog.Error("Error retrieving data for Game ID", "id", h, "error", err.Error())
			n.Close()
			return
		}
		if len(g.Meta.Name) == 0 && len(g.Teams) == 0 {
			slog.Error("Game is empty, ignoring", "id", h)
			n.Close()
			return
		}
		g.Meta.ID = uint64(h)
		for i := range m.Games {
			if m.Games[i].ID == g.Meta.ID {
				g.Meta.End = m.Games[i].End
				g.Meta.Start = m.Games[i].Start
				g.Meta.Status = m.Games[i].Status
				break
			}
		}
		s = &subscription{
			ID:      g.Meta.ID,
			new:     make(chan *websocket.Conn, 128),
			last:    g,
			clients: make([]*stream, 0, 1),
		}
		s.cache, _ = s.last.Delta(m.assets, nil)
		m.subs[g.Meta.ID] = s
	}
	atomic.StoreUint32(&s.stale, 0)
	_ = n.WriteJSON(s.cache)
	s.new <- n
}

func (m *Manager) Start(x context.Context) {
	for {
		select {
		case <-x.Done():
			m.close()
			return
		case <-m.tick.C:
			if atomic.LoadUint32(&m.running) == 0 {
				go m.startUpdate(x)
			}
		}
	}
}

func (m *Manager) update(x context.Context) {
	slog.Debug("Starting update..")
	if err := m.getJSON(x, "api/games", &m.Games); err != nil {
		slog.Error("Error occurred during update tick", "error", err.Error())
		return
	}
	for i := range m.Games {
		n := cleanSlugString(m.Games[i].Name)
		if !m.Games[i].Active() {
			delete(m.active, n)
			continue
		}
		if _, ok := m.active[n]; !ok {
			m.active[n] = m.Games[i].ID
			slog.Debug("Added Game name mapping to ID", "name", n, "id", m.Games[i].ID)
		}
	}
	select {
	case <-x.Done():
		return
	default:
		break
	}
	var r []uint64
	for _, s := range m.subs {
		if len(s.clients) == 0 {
			if atomic.LoadUint32(&s.stale) == 1 {
				r = append(r, s.ID)
				continue
			}
			atomic.StoreUint32(&s.stale, 1)
		}
		select {
		case <-x.Done():
			return
		default:
		}
		s.update(x, m)
	}
	for i := range r {
		select {
		case <-x.Done():
			return
		default:
		}
		slog.Debug("Removing unused subscription for Game", "id", r[i])
		close(m.subs[r[i]].new)
		delete(m.subs, r[i])
	}
	slog.Debug("Update finished", "games_count", len(m.Games))
}

func (h *hello) UnmarshalJSON(b []byte) error {
	var m map[string]uint64
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	v, ok := m["game"]
	if !ok {
		return errMissingGame
	}
	*h = hello(v)
	return nil
}

func (m *Manager) startUpdate(x context.Context) {
	atomic.StoreUint32(&m.running, 1)
	c, f := context.WithTimeout(x, m.timeout)
	go func(y context.Context, w context.CancelFunc, q *Manager) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic occurred during manager tick", "error", err)
				w()
			}
		}()
		q.update(y)
		w()
	}(c, f, m)
	<-c.Done()
	if c.Err() == context.DeadlineExceeded {
		slog.Warn("Collection update function ran over timeout", "timeout", m.timeout.String())
	}
	f()
	atomic.StoreUint32(&m.running, 0)
}

func (s *subscription) update(x context.Context, m *Manager) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Game subscription update function recovered from a panic", "error", err)
		}
	}()
	for len(s.new) > 0 {
		s.clients = append(s.clients, &stream{<-s.new, true})
	}
	select {
	case <-x.Done():
		return
	default:
	}
	slog.Debug("Checking for update for subscribed Game", "id", s.ID)
	var g game
	if err := m.getJSON(x, "api/games/"+strconv.FormatUint(s.ID, 10)+"/scoreboard", &g); err != nil {
		slog.Error("Error retrieving data for Game ID", "id", s.ID, "error", err.Error())
		return
	}
	g.Meta.ID = s.ID
	for i := range m.Games {
		if m.Games[i].ID == g.Meta.ID {
			g.Meta.End = m.Games[i].End
			g.Meta.Start = m.Games[i].Start
			g.Meta.Status = m.Games[i].Status
			break
		}
	}
	select {
	case <-x.Done():
		return
	default:
	}
	var u []update
	slog.Debug("Running game comparison on Game", "id", s.ID)
	s.cache, u = g.Delta(m.assets, &s.last)
	s.last = g
	if len(u) > 0 {
		slog.Debug("Updates detected in Game, updating clients", "updates_count", len(u), "id", s.ID)
		r := make([]*stream, 0, len(s.clients))
		for i := range s.clients {
			select {
			case <-x.Done():
				return
			default:
			}
			if i > len(s.clients) {
				return
			}
			if !s.clients[i].ok {
				s.clients[i].Close()
				continue
			}
			s.clients[i].ok = false
			if err := s.clients[i].WriteJSON(u); err != nil {
				slog.Error("Received error by client, removing", "remote", s.clients[i].RemoteAddr().String(), "error", err.Error())
				s.clients[i].Close()
				continue
			}
			s.clients[i].ok = true
			r = append(r, s.clients[i])
		}
		s.clients = r
	}
}

func (m Manager) get(x context.Context, u string) ([]byte, error) {
	reqUrl, err := url.Parse(m.url.String())
	if err != nil {
		return nil, err
	}
	reqUrl.Path = path.Join(reqUrl.Path, u)
	c, f := context.WithTimeout(x, m.timeout)
	defer f()
	r, err := http.NewRequestWithContext(c, http.MethodGet, reqUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	o, err := m.client.Do(r)
	if err != nil {
		return nil, err
	}
	if o.Body == nil {
		return nil, errors.New("request returned an empty body")
	}
	defer o.Body.Close()
	if o.StatusCode >= 400 {
		return nil, errors.New("request returned non-success status code: " + strconv.Itoa(o.StatusCode))
	}
	b, err := io.ReadAll(o.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (m Manager) getJSON(x context.Context, u string, o interface{}) error {
	r, err := m.get(x, u)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r, &o); err != nil {
		return err
	}
	return nil
}

func New(burl, d string, tick, t time.Duration) (*Manager, error) {
	u, err := url.Parse(burl)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() {
		u.Scheme = "http"
	}
	if len(d) == 0 {
		d = "/"
	}
	m := &Manager{
		url:    *u,
		subs:   make(map[uint64]*subscription),
		tick:   time.NewTicker(tick),
		active: make(map[string]uint64),
		assets: d,
		client: &http.Client{
			Timeout: t,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: t, KeepAlive: t}).DialContext,
				IdleConnTimeout:       t,
				TLSHandshakeTimeout:   t,
				ExpectContinueTimeout: t,
				ResponseHeaderTimeout: t,
			},
		},
		timeout: t,
	}
	return m, nil
}

func (m *Manager) URL() *url.URL {
	return &m.url
}

