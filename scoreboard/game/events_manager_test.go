package game

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
