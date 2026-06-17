package game

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompareTweetsRemovalAndUpdatePaths(t *testing.T) {
	p := new(planner)
	old := &game{
		Tweets: []tweet{
			{ID: 1, User: "u1", UserName: "user1", UserPhoto: "pic1", Text: "old"},
			{ID: 2, User: "u2", UserName: "user2", UserPhoto: "pic2", Text: "remove"},
		},
	}
	current := game{
		Tweets: []tweet{
			{ID: 1, User: "u1", UserName: "user1", UserPhoto: "pic1", Text: "new"},
		},
	}
	old.hashTweets(new(hasher))
	current.hashTweets(new(hasher))
	current.compareTweets(p, old)
	if !containsUpdateID(p.Delta, "tweet-t2") {
		t.Fatalf("expected removed tweet id delta")
	}
	if !containsUpdateID(p.Create, "tweet-t1") {
		t.Fatalf("expected tweet update in create payload")
	}
}

func TestEventsCompareRemovals(t *testing.T) {
	p := new(planner)
	cur := events{
		Current: []event{
			{ID: 1, Type: 0, Data: map[string]string{"k": "v"}},
		},
	}
	old := events{
		Current: []event{
			{ID: 1, Type: 1, Data: map[string]string{"k": "v"}},
			{ID: 2, Type: 2, Data: map[string]string{"x": "y"}},
		},
	}
	cur.Hash(new(hasher))
	old.Hash(new(hasher))
	cur.Compare(p, old)
	foundRemoved := false
	for i := range p.Delta {
		if p.Delta[i].ID == "2" && p.Delta[i].Event && p.Delta[i].Remove {
			foundRemoved = true
			break
		}
	}
	if !foundRemoved {
		t.Fatalf("expected removed event delta for id=2")
	}
}

func TestManagerGetParseError(t *testing.T) {
	m, err := New("http://scorebot", "", time.Second, time.Second)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	m.url = url.URL{Scheme: "http", Host: "scorebot", Path: "%"}
	if _, err := m.get(context.Background(), "api/games"); err == nil {
		t.Fatalf("expected parse error for malformed base URL path")
	}
}

func TestManagerStartUpdateNoDeadline(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer api.Close()

	m, err := New(api.URL, "", time.Millisecond, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("manager new: %v", err)
	}
	m.startUpdate(context.Background())
	if atomic.LoadUint32(&m.running) != 0 {
		t.Fatalf("expected running flag reset after successful update")
	}
}

func TestManagerStartUpdateRecoversFromPanic(t *testing.T) {
	m := &Manager{
		subs: map[uint64]*subscription{
			1: nil,
		},
		active:  make(map[string]uint64),
		tick:    time.NewTicker(time.Hour),
		client:  &http.Client{Timeout: time.Second},
		timeout: 25 * time.Millisecond,
	}
	defer m.tick.Stop()

	m.startUpdate(context.Background())
	if atomic.LoadUint32(&m.running) != 0 {
		t.Fatalf("expected running flag reset after panic recovery")
	}
}
