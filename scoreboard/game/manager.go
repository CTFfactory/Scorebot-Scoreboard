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
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	errMissingGame      = errors.New("game ID is missing from JSON data")
	errEmptyGamePayload = errors.New("game is empty")
)

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
	mu      sync.RWMutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
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

func isSlugSafeByte(b byte) bool {
	return strings.IndexByte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-._", b) >= 0
}

func cleanSlugString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range s {
		if isSlugSafeByte(s[i]) {
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte('-')
	}
	return b.String()
}

func (m *Manager) Game(s string) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active[strings.ToLower(cleanSlugString(s))]
}

func (m *Manager) readHello(n *websocket.Conn) (hello, error) {
	var h hello
	if err := n.ReadJSON(&h); err != nil {
		return 0, err
	}
	return h, nil
}

func (m *Manager) subscriptionByID(id uint64) *subscription {
	m.mu.RLock()
	s := m.subs[id]
	m.mu.RUnlock()
	return s
}

func (m *Manager) hydrateGameMeta(g *game) {
	m.mu.RLock()
	for i := range m.Games {
		if m.Games[i].ID == g.Meta.ID {
			g.Meta.End = m.Games[i].End
			g.Meta.Start = m.Games[i].Start
			g.Meta.Status = m.Games[i].Status
			break
		}
	}
	m.mu.RUnlock()
}

func (m *Manager) fetchGameForSubscription(id uint64) (game, error) {
	var g game
	if err := m.getJSON(context.Background(), "api/games/"+strconv.FormatUint(id, 10)+"/scoreboard", &g); err != nil {
		return g, err
	}
	if len(g.Meta.Name) == 0 && len(g.Teams) == 0 {
		return g, errEmptyGamePayload
	}
	g.Meta.ID = id
	m.hydrateGameMeta(&g)
	return g, nil
}

func (m *Manager) ensureSubscription(g game) *subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.subs[g.Meta.ID]; ok && s != nil {
		return s
	}
	s := &subscription{
		ID:      g.Meta.ID,
		new:     make(chan *websocket.Conn, 128),
		last:    g,
		clients: make([]*stream, 0, 1),
	}
	s.cache, _ = s.last.Delta(m.assets, nil)
	m.subs[g.Meta.ID] = s
	return s
}

func (m *Manager) resolveSubscription(id uint64, remote string) (*subscription, bool) {
	if s := m.subscriptionByID(id); s != nil {
		return s, true
	}
	slog.Debug("Checking Game ID requested by remote", "id", id, "remote", remote)
	g, err := m.fetchGameForSubscription(id)
	if err != nil {
		if errors.Is(err, errEmptyGamePayload) {
			slog.Error("Game is empty, ignoring", "id", id)
			return nil, false
		}
		slog.Error("Error retrieving data for Game ID", "id", id, "error", err.Error())
		return nil, false
	}
	return m.ensureSubscription(g), true
}

func (m *Manager) queueClientConnection(s *subscription, n *websocket.Conn) {
	atomic.StoreUint32(&s.stale, 0)
	m.mu.RLock()
	cache := append([]update(nil), s.cache...)
	m.mu.RUnlock()
	_ = n.WriteJSON(cache)
	m.mu.Lock()
	if r, ok := m.subs[s.ID]; !ok || r != s {
		m.mu.Unlock()
		n.Close()
		return
	}
	select {
	case s.new <- n:
		m.mu.Unlock()
	default:
		m.mu.Unlock()
		n.Close()
	}
}

func (m *Manager) New(n *websocket.Conn) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Collection newclient function recovered from a panic", "error", err)
		}
	}()
	remote := n.RemoteAddr().String()
	slog.Debug("Received connection, listening for Hello", "remote", remote)
	h, err := m.readHello(n)
	if err != nil {
		slog.Error("Could not read Hello message", "remote", remote, "error", err.Error())
		n.Close()
		return
	}
	slog.Debug("Received Hello with requested Game ID", "id", h, "remote", remote)
	s, ok := m.resolveSubscription(uint64(h), remote)
	if !ok || s == nil {
		n.Close()
		return
	}
	m.queueClientConnection(s, n)
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

func contextDone(x context.Context) bool {
	select {
	case <-x.Done():
		return true
	default:
		return false
	}
}

func (m *Manager) fetchGames(x context.Context) ([]meta, bool) {
	var games []meta
	if err := m.getJSON(x, "api/games", &games); err != nil {
		slog.Error("Error occurred during update tick", "error", err.Error())
		return nil, false
	}
	return games, true
}

func (m *Manager) updateActiveGamesLocked() {
	for i := range m.Games {
		n := cleanSlugString(m.Games[i].Name)
		if !m.Games[i].Active() {
			delete(m.active, n)
			continue
		}
		if _, ok := m.active[n]; ok {
			continue
		}
		m.active[n] = m.Games[i].ID
		slog.Debug("Added Game name mapping to ID", "name", n, "id", m.Games[i].ID)
	}
}

func (m *Manager) updateSubscriptionsLocked(x context.Context) ([]uint64, bool) {
	var stale []uint64
	for _, s := range m.subs {
		if len(s.clients) == 0 {
			if atomic.LoadUint32(&s.stale) == 1 {
				stale = append(stale, s.ID)
				continue
			}
			atomic.StoreUint32(&s.stale, 1)
		}
		if contextDone(x) {
			return stale, false
		}
		s.update(x, m)
	}
	return stale, true
}

func (m *Manager) removeStaleSubscriptionsLocked(x context.Context, stale []uint64) bool {
	for i := range stale {
		if contextDone(x) {
			return false
		}
		slog.Debug("Removing unused subscription for Game", "id", stale[i])
		close(m.subs[stale[i]].new)
		delete(m.subs, stale[i])
	}
	return true
}

func (m *Manager) update(x context.Context) {
	slog.Debug("Starting update..")
	games, ok := m.fetchGames(x)
	if !ok {
		return
	}
	m.mu.Lock()
	m.Games = games
	defer m.mu.Unlock()
	m.updateActiveGamesLocked()
	if contextDone(x) {
		return
	}
	stale, ok := m.updateSubscriptionsLocked(x)
	if !ok {
		return
	}
	if !m.removeStaleSubscriptionsLocked(x, stale) {
		return
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

func (s *subscription) queueClients() {
	for len(s.new) > 0 {
		s.clients = append(s.clients, &stream{<-s.new, true})
	}
}

func (s *subscription) fetchGame(x context.Context, m *Manager) (game, bool) {
	var g game
	if err := m.getJSON(x, "api/games/"+strconv.FormatUint(s.ID, 10)+"/scoreboard", &g); err != nil {
		slog.Error("Error retrieving data for Game ID", "id", s.ID, "error", err.Error())
		return g, false
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
	return g, true
}

func (s *subscription) buildDelta(g game, assets string) []update {
	var u []update
	s.cache, u = g.Delta(assets, &s.last)
	s.last = g
	return u
}

func (s *subscription) writeUpdates(x context.Context, u []update) {
	r := make([]*stream, 0, len(s.clients))
	for i := range s.clients {
		if contextDone(x) {
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

func (s *subscription) update(x context.Context, m *Manager) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Game subscription update function recovered from a panic", "error", err)
		}
	}()
	s.queueClients()
	if contextDone(x) {
		return
	}
	slog.Debug("Checking for update for subscribed Game", "id", s.ID)
	g, ok := s.fetchGame(x, m)
	if !ok {
		return
	}
	if contextDone(x) {
		return
	}
	slog.Debug("Running game comparison on Game", "id", s.ID)
	u := s.buildDelta(g, m.assets)
	if len(u) > 0 {
		slog.Debug("Updates detected in Game, updating clients", "updates_count", len(u), "id", s.ID)
		s.writeUpdates(x, u)
	}
}

func (m *Manager) requestURL(endpoint string) (*url.URL, error) {
	reqURL, err := url.Parse(m.url.String())
	if err != nil {
		return nil, err
	}
	reqURL.Path = path.Join(reqURL.Path, endpoint)
	if !strings.HasPrefix(reqURL.Path, "/") {
		reqURL.Path = "/" + reqURL.Path
	}
	return reqURL, nil
}

func buildGetRequest(ctx context.Context, reqURL *url.URL) *http.Request {
	return (&http.Request{
		Method:     http.MethodGet,
		URL:        reqURL,
		Host:       reqURL.Host,
		Header:     make(http.Header),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}).WithContext(ctx)
}

func (m *Manager) doRequest(ctx context.Context, endpoint string) (*http.Response, context.CancelFunc, error) {
	reqURL, err := m.requestURL(endpoint)
	if err != nil {
		return nil, nil, err
	}
	c, f := context.WithTimeout(ctx, m.timeout)
	r := buildGetRequest(c, reqURL)
	o, err := m.client.Do(r)
	if err != nil {
		f()
		return nil, nil, err
	}
	return o, f, nil
}

func validateResponse(o *http.Response) error {
	if o.Body == nil {
		return errors.New("request returned an empty body")
	}
	if o.StatusCode >= 400 {
		return errors.New("request returned non-success status code: " + strconv.Itoa(o.StatusCode))
	}
	return nil
}

func (m *Manager) get(x context.Context, u string) ([]byte, error) {
	o, f, err := m.doRequest(x, u)
	if err != nil {
		return nil, err
	}
	defer f()
	if err := validateResponse(o); err != nil {
		if o.Body != nil {
			o.Body.Close()
		}
		return nil, err
	}
	defer o.Body.Close()
	return io.ReadAll(o.Body)
}

func (m *Manager) getJSON(x context.Context, u string, o interface{}) error {
	r, err := m.get(x, u)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r, o); err != nil {
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
		d = u.String()
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
