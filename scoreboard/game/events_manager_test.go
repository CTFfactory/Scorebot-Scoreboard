package game

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func websocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn, func()) {
	t.Helper()
	serverConn := make(chan *websocket.Conn, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		serverConn <- c
	}))

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		wsServer.Close()
		t.Fatalf("dial websocket: %v", err)
	}

	select {
	case server := <-serverConn:
		cleanup := func() {
			_ = client.Close()
			_ = server.Close()
			wsServer.Close()
		}
		return server, client, cleanup
	case <-time.After(time.Second):
		_ = client.Close()
		wsServer.Close()
		t.Fatalf("timeout waiting for server websocket connection")
	}
	return nil, nil, nil
}

func TestEventsHashAndCompare(t *testing.T) {
	var h hasher
	e1 := events{Current: []event{{ID: 1, Type: 2, Data: map[string]string{"k": "v"}}}}
	e2 := events{Current: []event{{ID: 1, Type: 2, Data: map[string]string{"k": "v"}}}}
	_ = e1.Hash(&h)
	_ = e2.Hash(&h)

	p := new(planner)
	e1.Compare(p, e2)
	if len(p.Create) == 0 {
		t.Fatalf("expected create events for equivalent hashes")
	}

	p = new(planner)
	e1.Compare(p, events{})
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta events when comparing against empty events")
	}
}

func TestSetWindowEventBehavior(t *testing.T) {
	e := events{Window: event{ID: 1, Type: 1}}
	p := new(planner)

	e.setWindowEvent(p, event{ID: 2, Type: 2})
	if e.Window.ID != 2 {
		t.Fatalf("expected window event update")
	}
	if len(p.Delta) != 1 {
		t.Fatalf("expected one removal delta when replacing window event")
	}

	p = new(planner)
	e.setWindowEvent(p, event{ID: 2, Type: 2})
	if len(p.Delta) != 0 {
		t.Fatalf("expected no-op when same window event id")
	}
}

func TestTweetComparisonPaths(t *testing.T) {
	g := game{
		Tweets: []tweet{
			{ID: 1, User: "u", UserName: "user", UserPhoto: "pic", Text: "txt", Images: []string{"i1"}},
		},
	}
	p := new(planner)
	g.compareTweets(p, nil)
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for new tweet set")
	}

	var h hasher
	_ = g.hashTweets(&h)
	old := g
	_ = old.hashTweets(&h)

	p = new(planner)
	g.compareTweets(p, &old)
	if len(p.Create) == 0 {
		t.Fatalf("expected create updates for equivalent tweets")
	}
}

func TestManagerNewAndGameLookup(t *testing.T) {
	m, err := New("http://scorebot", "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	if m.assets != "http://scorebot" {
		t.Fatalf("expected assets to default to base URL, got %q", m.assets)
	}
	m.active["te-am"] = 9
	if got := m.Game("Te am"); got != 9 {
		t.Fatalf("expected normalized game lookup to return 9, got %d", got)
	}

	m2, err := New("scorebot", "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new non-abs: %v", err)
	}
	if got := m2.url.String(); got != "http://scorebot" {
		t.Fatalf("expected normalized non-abs URL, got %q", got)
	}

	m3, err := New("http://scorebot", "https://assets.example", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new with assets: %v", err)
	}
	if m3.assets != "https://assets.example" {
		t.Fatalf("expected explicit assets override, got %q", m3.assets)
	}
}

func TestManagerGetAndJSONBehavior(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = io.WriteString(w, `{"value":"set"}`)
		case "/bad":
			http.Error(w, "bad", http.StatusBadRequest)
		case "/invalid":
			_, _ = io.WriteString(w, `{"value":`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m, err := New(srv.URL, "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}

	body, err := m.get(context.Background(), "ok")
	if err != nil {
		t.Fatalf("manager get ok: %v", err)
	}
	if !strings.Contains(string(body), `"value":"set"`) {
		t.Fatalf("unexpected body: %q", string(body))
	}

	if _, err := m.get(context.Background(), "bad"); err == nil {
		t.Fatalf("expected non-success status error")
	}

	var out struct {
		Value string `json:"value"`
	}
	if err := m.getJSON(context.Background(), "ok", &out); err != nil {
		t.Fatalf("manager getJSON ok: %v", err)
	}
	if out.Value != "set" {
		t.Fatalf("expected decoded value, got %q", out.Value)
	}

	if err := m.getJSON(context.Background(), "invalid", &out); err == nil {
		t.Fatalf("expected JSON decode error")
	}
}

func TestManagerHelperFunctions(t *testing.T) {
	t.Run("hydrate game meta", func(t *testing.T) {
		now := time.Now().UTC()
		m := &Manager{
			Games: []meta{
				{ID: 1, Start: now, End: now.Add(time.Hour), Status: running},
			},
		}
		g := game{Meta: meta{ID: 1}}
		m.hydrateGameMeta(&g)
		if g.Meta.Status != running || g.Meta.Start.IsZero() || g.Meta.End.IsZero() {
			t.Fatalf("expected game meta fields to be hydrated for matching ID")
		}

		other := game{Meta: meta{ID: 2, Status: paused}}
		m.hydrateGameMeta(&other)
		if other.Meta.Status != paused {
			t.Fatalf("expected non-matching ID meta to remain unchanged")
		}
	})

	t.Run("update active games locked", func(t *testing.T) {
		m := &Manager{
			active: map[string]uint64{
				"inactive-game": 99,
				"existing":      5,
			},
			Games: []meta{
				{ID: 99, Name: "inactive game", Status: cancelled},
				{ID: 5, Name: "existing", Status: running},
				{ID: 7, Name: "new game", Status: running},
			},
		}
		m.updateActiveGamesLocked()
		if _, ok := m.active["inactive-game"]; ok {
			t.Fatalf("expected inactive game mapping to be removed")
		}
		if m.active["existing"] != 5 {
			t.Fatalf("expected existing active mapping to remain unchanged")
		}
		if m.active["new-game"] != 7 {
			t.Fatalf("expected new active game mapping to be added")
		}
	})

	t.Run("request/do/validate helpers", func(t *testing.T) {
		m, err := New("http://example", "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		reqURL, err := m.requestURL("api/games")
		if err != nil {
			t.Fatalf("requestURL success path failed: %v", err)
		}
		if !strings.Contains(reqURL.String(), "/api/games") {
			t.Fatalf("expected joined path in request URL, got %q", reqURL.String())
		}

		m.url.Scheme = "%"
		if _, err := m.requestURL("api/games"); err == nil {
			t.Fatalf("expected requestURL parse error for invalid base URL path")
		}

		m2, err := New("http://example", "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		m2.url.Scheme = "%"
		if _, cancel, err := m2.doRequest(context.Background(), "api/games"); err == nil || cancel != nil {
			t.Fatalf("expected doRequest requestURL error with nil cancel func")
		}
		m2.url.Scheme = "http"
		m2.client = &http.Client{
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("transport boom")
			}),
		}
		if _, cancel, err := m2.doRequest(context.Background(), "api/games"); err == nil || cancel != nil {
			t.Fatalf("expected doRequest transport error with nil cancel func")
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		defer srv.Close()
		m3, err := New(srv.URL, "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		resp, cancel, err := m3.doRequest(context.Background(), "api/games")
		if err != nil {
			t.Fatalf("expected doRequest success path: %v", err)
		}
		if cancel == nil {
			t.Fatalf("expected non-nil cancel func on doRequest success")
		}
		_ = resp.Body.Close()
		cancel()

		if err := validateResponse(&http.Response{StatusCode: http.StatusOK}); err == nil {
			t.Fatalf("expected validateResponse to fail on nil body")
		}
		if err := validateResponse(&http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("x"))}); err == nil {
			t.Fatalf("expected validateResponse to fail on non-success status")
		}
		if err := validateResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("x"))}); err != nil {
			t.Fatalf("expected validateResponse success, got %v", err)
		}
	})

	t.Run("queue client connection branches", func(t *testing.T) {
		t.Run("success enqueue", func(t *testing.T) {
			server, client, cleanup := websocketPair(t)
			defer cleanup()
			m := &Manager{subs: make(map[uint64]*subscription)}
			s := &subscription{
				ID:      1,
				new:     make(chan *websocket.Conn, 1),
				cache:   []update{{ID: "cached", Value: "v"}},
				clients: make([]*stream, 0),
			}
			m.subs[1] = s
			m.queueClientConnection(s, server)
			if len(s.new) != 1 {
				t.Fatalf("expected connection to be queued")
			}
			_ = client.SetReadDeadline(time.Now().Add(time.Second))
			var payload []update
			if err := client.ReadJSON(&payload); err != nil {
				t.Fatalf("expected cached payload to be written: %v", err)
			}
			if len(payload) == 0 {
				t.Fatalf("expected non-empty cached payload")
			}
		})

		t.Run("subscription mismatch closes connection", func(t *testing.T) {
			server, _, cleanup := websocketPair(t)
			defer cleanup()
			m := &Manager{subs: make(map[uint64]*subscription)}
			target := &subscription{ID: 2, new: make(chan *websocket.Conn, 1)}
			other := &subscription{ID: 2, new: make(chan *websocket.Conn, 1)}
			m.subs[2] = other
			m.queueClientConnection(target, server)
			if len(target.new) != 0 || len(other.new) != 0 {
				t.Fatalf("expected mismatch path to avoid enqueue")
			}
		})

		t.Run("queue full drops connection", func(t *testing.T) {
			server, _, cleanup := websocketPair(t)
			defer cleanup()
			m := &Manager{subs: make(map[uint64]*subscription)}
			s := &subscription{
				ID:      3,
				new:     make(chan *websocket.Conn, 1),
				cache:   []update{{ID: "cached", Value: "v"}},
				clients: make([]*stream, 0),
			}
			s.new <- nil
			m.subs[3] = s
			m.queueClientConnection(s, server)
			if len(s.new) != 1 {
				t.Fatalf("expected queue to remain full")
			}
		})
	})
}

func TestManagerNewWebsocketSubscription(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/games/1/scoreboard":
			_, _ = io.WriteString(w, `{"name":"Game One","mode":0,"teams":[{"id":1,"name":"Blue"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	m, err := New(api.URL, "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		m.New(c)
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer client.Close()

	if err := client.WriteJSON(map[string]uint64{"game": 1}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var u []update
	if err := client.ReadJSON(&u); err != nil {
		t.Fatalf("read initial cache: %v", err)
	}
	if len(u) == 0 {
		t.Fatalf("expected initial cache updates")
	}
}

func TestManagerNewErrorBranches(t *testing.T) {
	t.Run("invalid hello payload", func(t *testing.T) {
		m, err := New("http://scorebot", "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		server, client, cleanup := websocketPair(t)
		defer cleanup()
		done := make(chan struct{})
		go func() {
			m.New(server)
			close(done)
		}()
		if err := client.WriteMessage(websocket.TextMessage, []byte(`{"invalid":"payload"}`)); err != nil {
			t.Fatalf("write invalid hello: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("manager.New did not return on invalid hello payload")
		}
	})

	t.Run("upstream score API error", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer api.Close()

		m, err := New(api.URL, "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		server, client, cleanup := websocketPair(t)
		defer cleanup()

		done := make(chan struct{})
		go func() {
			m.New(server)
			close(done)
		}()
		if err := client.WriteJSON(map[string]uint64{"game": 5}); err != nil {
			t.Fatalf("write hello: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("manager.New did not return after upstream error")
		}
		if len(m.subs) != 0 {
			t.Fatalf("expected no subscriptions after upstream error")
		}
	})

	t.Run("empty game payload", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{}`)
		}))
		defer api.Close()

		m, err := New(api.URL, "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		server, client, cleanup := websocketPair(t)
		defer cleanup()

		done := make(chan struct{})
		go func() {
			m.New(server)
			close(done)
		}()
		if err := client.WriteJSON(map[string]uint64{"game": 3}); err != nil {
			t.Fatalf("write hello: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("manager.New did not return for empty game payload")
		}
		if len(m.subs) != 0 {
			t.Fatalf("expected no subscriptions for empty game payload")
		}
	})

	t.Run("full queue drops incoming conn", func(t *testing.T) {
		m, err := New("http://scorebot", "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		sub := &subscription{
			ID:      1,
			new:     make(chan *websocket.Conn, 1),
			cache:   []update{{ID: "cached", Value: "1"}},
			clients: make([]*stream, 0),
		}
		sub.new <- nil // fill channel to force default branch in New()
		m.subs[1] = sub

		server, client, cleanup := websocketPair(t)
		defer cleanup()
		done := make(chan struct{})
		go func() {
			m.New(server)
			close(done)
		}()
		if err := client.WriteJSON(map[string]uint64{"game": 1}); err != nil {
			t.Fatalf("write hello: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("manager.New did not return when queue is full")
		}
		if len(sub.new) != 1 {
			t.Fatalf("expected queue to remain full")
		}
	})
}

func TestManagerUpdateAndLifecycle(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/games":
			_, _ = io.WriteString(w, `[{"id":1,"name":"Game One","mode":0,"status":1}]`)
		case "/api/games/1/scoreboard":
			_, _ = io.WriteString(w, `{"name":"Game One","mode":0,"teams":[{"id":1,"name":"Blue"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	m, err := New(api.URL, "", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}

	m.subs[1] = &subscription{
		ID:      1,
		new:     make(chan *websocket.Conn, 1),
		clients: make([]*stream, 0),
	}

	m.update(context.Background())
	if len(m.Games) != 1 {
		t.Fatalf("expected one game after update")
	}
	if len(m.subs) != 1 {
		t.Fatalf("expected stale-marked subscription to remain after first pass")
	}

	m.update(context.Background())
	if len(m.subs) != 0 {
		t.Fatalf("expected stale subscription cleanup after second pass")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Start(ctx)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("manager start did not stop after cancel")
	}
}

func TestManagerClose(t *testing.T) {
	m := &Manager{
		subs: map[uint64]*subscription{
			1: {
				ID:      1,
				new:     make(chan *websocket.Conn, 1),
				clients: make([]*stream, 0),
			},
		},
		tick: time.NewTicker(time.Hour),
	}
	m.close()
	if len(m.subs) != 0 {
		t.Fatalf("expected close to clear subscriptions")
	}
}

func TestManagerCloseWithClients(t *testing.T) {
	server, client, cleanup := websocketPair(t)
	defer cleanup()
	m := &Manager{
		subs: map[uint64]*subscription{
			1: {
				ID:  1,
				new: make(chan *websocket.Conn, 1),
				clients: []*stream{
					{Conn: server, ok: true},
				},
			},
		},
		tick: time.NewTicker(time.Hour),
	}
	m.close()
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatalf("expected client connection to close")
	}
}

func TestSubscriptionUpdatePaths(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/games/1/scoreboard":
			_, _ = io.WriteString(w, `{"name":"Game One","mode":0,"teams":[{"id":1,"name":"Blue"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	m, err := New(api.URL, "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	m.Games = []meta{{ID: 1, Status: running}}

	t.Run("drain queued connections and send updates", func(t *testing.T) {
		server, client, cleanup := websocketPair(t)
		defer cleanup()
		s := &subscription{
			ID:      1,
			new:     make(chan *websocket.Conn, 1),
			clients: make([]*stream, 0),
		}
		s.new <- server
		s.update(context.Background(), m)
		if len(s.clients) != 1 {
			t.Fatalf("expected one active client after update, got %d", len(s.clients))
		}
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		var u []update
		if err := client.ReadJSON(&u); err != nil {
			t.Fatalf("expected update payload to queued websocket client: %v", err)
		}
		if len(u) == 0 {
			t.Fatalf("expected non-empty update payload")
		}
	})

	t.Run("remove unhealthy clients", func(t *testing.T) {
		server, client, cleanup := websocketPair(t)
		defer cleanup()
		_ = client.Close()
		time.Sleep(10 * time.Millisecond)
		s := &subscription{
			ID:  1,
			new: make(chan *websocket.Conn, 1),
			clients: []*stream{
				{Conn: server, ok: false},
			},
		}
		s.update(context.Background(), m)
		if len(s.clients) != 0 {
			t.Fatalf("expected unhealthy client to be dropped")
		}
	})

	t.Run("remove clients on write error", func(t *testing.T) {
		server, client, cleanup := websocketPair(t)
		defer cleanup()
		_ = client.Close()
		_ = server.Close()
		s := &subscription{
			ID:  1,
			new: make(chan *websocket.Conn, 1),
			clients: []*stream{
				{Conn: server, ok: true},
			},
		}
		s.update(context.Background(), m)
		if len(s.clients) != 0 {
			t.Fatalf("expected client removal on write failure")
		}
	})

	t.Run("cancelled context exits early", func(t *testing.T) {
		s := &subscription{
			ID:      1,
			new:     make(chan *websocket.Conn, 1),
			clients: make([]*stream, 0),
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.update(ctx, m)
	})
}

func TestManagerGetRequestBuildError(t *testing.T) {
	m, err := New("http://scorebot", "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	m.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, &net.AddrError{Err: "dial fail"}
		}),
	}
	if _, err := m.get(context.Background(), "api/games"); err == nil {
		t.Fatalf("expected client transport error")
	}
}

func TestManagerGetResponseBodyErrors(t *testing.T) {
	t.Run("body read error", func(t *testing.T) {
		m, err := New("http://scorebot", "", time.Second, time.Second)
		if err != nil {
			t.Fatalf("manager new: %v", err)
		}
		m.client = &http.Client{
			Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errorReadCloser{err: errors.New("read fail")},
				}, nil
			}),
		}
		if _, err := m.get(context.Background(), "api/games"); err == nil {
			t.Fatalf("expected body read error")
		}
	})
}

func TestManagerNewInvalidURL(t *testing.T) {
	if _, err := New("http://%", "", time.Second, time.Second); err == nil {
		t.Fatalf("expected URL parse error")
	}
}

func TestManagerStartUpdateDeadline(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer api.Close()

	m, err := New(api.URL, "", time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	m.startUpdate(context.Background())
	if atomic.LoadUint32(&m.running) != 0 {
		t.Fatalf("expected manager running flag reset")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errorReadCloser struct {
	err error
}

func (e errorReadCloser) Read([]byte) (int, error) {
	return 0, e.err
}

func (e errorReadCloser) Close() error {
	return nil
}
