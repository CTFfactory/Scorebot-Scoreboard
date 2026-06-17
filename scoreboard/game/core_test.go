package game

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCleanSlugString(t *testing.T) {
	got := cleanSlugString("Te am/Name!?")
	if got != "Te-am-Name--" {
		t.Fatalf("unexpected cleaned slug: %q", got)
	}
}

func TestHelloUnmarshalJSON(t *testing.T) {
	var h hello
	if err := json.Unmarshal([]byte(`{"game":42}`), &h); err != nil {
		t.Fatalf("unmarshal hello: %v", err)
	}
	if uint64(h) != 42 {
		t.Fatalf("expected game id 42, got %d", h)
	}
	if err := json.Unmarshal([]byte(`{"nope":42}`), &h); err != errMissingGame {
		t.Fatalf("expected errMissingGame, got %v", err)
	}
}

func TestModeStatusMetaMethods(t *testing.T) {
	modeCases := map[mode]string{
		redBlue:  "Red vs Blue",
		blueBlue: "Blue vs Blue",
		king:     "King of the Hill",
		rush:     "Rush",
		defend:   "Server Defence",
	}
	for k, want := range modeCases {
		if got := k.String(); got != want {
			t.Fatalf("mode %d expected %q got %q", k, want, got)
		}
	}
	if mode(99).String() != "Unknown" {
		t.Fatalf("unexpected unknown mode string")
	}

	statusCases := map[status]string{
		stopped:   "Stopped",
		running:   "Running",
		paused:    "Paused",
		cancelled: "Cancelled",
		completed: "Completed",
	}
	for k, want := range statusCases {
		if got := k.String(); got != want {
			t.Fatalf("status %d expected %q got %q", k, want, got)
		}
	}
	if status(99).String() != "Unknown" {
		t.Fatalf("unexpected unknown status string")
	}

	m := meta{Status: running}
	if !m.Active() || !m.Display() {
		t.Fatalf("running meta should be active and displayable")
	}
	m.Status = cancelled
	if m.Active() || m.Display() {
		t.Fatalf("cancelled meta should be inactive and hidden")
	}
	m.Status = completed
	if m.Active() {
		t.Fatalf("completed meta should be inactive")
	}

	if (meta{}).String() != "" {
		t.Fatalf("zero start meta should render empty")
	}
	m = meta{
		Start: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC),
		End:   time.Date(2026, 1, 2, 4, 5, 0, 0, time.UTC),
	}
	out := m.String()
	if !strings.Contains(out, "03:04 Jan 2 2026") || !strings.Contains(out, "04:05 Jan 2 2026") {
		t.Fatalf("unexpected meta string output: %q", out)
	}
	m.End = time.Time{}
	if one := m.String(); !strings.Contains(one, "03:04 Jan 2 2026") || strings.Contains(one, "to") {
		t.Fatalf("unexpected single-time meta output: %q", one)
	}
}

func TestStateAndProtocolJSON(t *testing.T) {
	var s state
	if err := json.Unmarshal([]byte(`"green"`), &s); err != nil {
		t.Fatalf("unmarshal green: %v", err)
	}
	if s != green || s.class() != "port" || s.String() != "rgb(40, 111, 36)" {
		t.Fatalf("unexpected green state behavior")
	}
	if err := json.Unmarshal([]byte(`"unknown"`), &s); err != nil {
		t.Fatalf("unmarshal unknown state: %v", err)
	}
	if s != red {
		t.Fatalf("unknown state should default to red")
	}
	if red.class() != "err" || yellow.class() != "warn" || state(255).class() != "port" {
		t.Fatalf("unexpected state class mapping")
	}
	if red.String() != "rgb(255, 0, 0)" || yellow.String() != "rgb(173, 164, 21)" || state(255).String() != "rgb(255, 0, 0)" {
		t.Fatalf("unexpected state color mapping")
	}

	var p protocol
	if err := json.Unmarshal([]byte(`"udp"`), &p); err != nil {
		t.Fatalf("unmarshal udp: %v", err)
	}
	if p != udp || p.String() != "udp" {
		t.Fatalf("unexpected udp protocol behavior")
	}
	if err := json.Unmarshal([]byte(`"unknown"`), &p); err != nil {
		t.Fatalf("unmarshal unknown protocol: %v", err)
	}
	if p != tcp {
		t.Fatalf("unknown protocol should default to tcp")
	}
	if err := json.Unmarshal([]byte(`"icmp"`), &p); err != nil {
		t.Fatalf("unmarshal icmp protocol: %v", err)
	}
	if p != icmp {
		t.Fatalf("expected icmp protocol, got %v", p)
	}
	if protocol(255).String() != "Unknown" {
		t.Fatalf("unexpected unknown protocol string")
	}
	if tcp.String() != "tcp" || icmp.String() != "icmp" {
		t.Fatalf("unexpected protocol String output")
	}
}

func TestStateAndProtocolAliasAndErrorInputs(t *testing.T) {
	stateCases := []struct {
		input string
		want  state
	}{
		{`"r"`, red},
		{`"fail"`, red},
		{`"y"`, yellow},
		{`"issue"`, yellow},
		{`"g"`, green},
		{`"ok"`, green},
	}
	for _, tc := range stateCases {
		var s state
		if err := json.Unmarshal([]byte(tc.input), &s); err != nil {
			t.Fatalf("unmarshal state %s: %v", tc.input, err)
		}
		if s != tc.want {
			t.Fatalf("state input %s expected %v got %v", tc.input, tc.want, s)
		}
	}
	var badState state
	if err := json.Unmarshal([]byte(`123`), &badState); err == nil {
		t.Fatalf("expected state unmarshal type error")
	}

	protoCases := []struct {
		input string
		want  protocol
	}{
		{`"t"`, tcp},
		{`"u"`, udp},
		{`"i"`, icmp},
		{`"p"`, icmp},
		{`"ping"`, icmp},
	}
	for _, tc := range protoCases {
		var p protocol
		if err := json.Unmarshal([]byte(tc.input), &p); err != nil {
			t.Fatalf("unmarshal protocol %s: %v", tc.input, err)
		}
		if p != tc.want {
			t.Fatalf("protocol input %s expected %v got %v", tc.input, tc.want, p)
		}
	}
	var badProtocol protocol
	if err := json.Unmarshal([]byte(`123`), &badProtocol); err == nil {
		t.Fatalf("expected protocol unmarshal type error")
	}
}

func TestGameUnmarshalJSON(t *testing.T) {
	in := `{
		"name":"Game Name",
		"mode":1,
		"credit":"Credits",
		"message":"Message",
		"teams":[{"id":2,"name":"Team B"},{"id":1,"name":"Team A"}],
		"events":[{"id":9,"type":1,"data":{"k":"v"}}]
	}`
	var g game
	if err := json.Unmarshal([]byte(in), &g); err != nil {
		t.Fatalf("unmarshal game: %v", err)
	}
	if g.Meta.Name != "Game Name" || g.Credit != "Credits" || g.Message != "Message" {
		t.Fatalf("unexpected game fields after unmarshal")
	}
	if len(g.Teams) != 2 || len(g.Events.Current) != 1 {
		t.Fatalf("unexpected teams/events counts")
	}
}

func TestGameUnmarshalJSONErrors(t *testing.T) {
	cases := []string{
		`{"name":123}`,
		`{"mode":"bad"}`,
		`{"credit":123}`,
		`{"message":123}`,
		`{"teams":"bad"}`,
		`{"events":"bad"}`,
	}
	for _, payload := range cases {
		var g game
		if err := json.Unmarshal([]byte(payload), &g); err == nil {
			t.Fatalf("expected unmarshal error for payload %s", payload)
		}
	}
}

func TestGameDeltaCurrentBehavior(t *testing.T) {
	g := game{
		Message: "m",
		Meta:    meta{ID: 7, Name: "Example", Mode: redBlue, Status: running},
		Teams: []team{
			{ID: 2, Name: "B", Logo: "/logo-b.png"},
			{ID: 1, Name: "A", Logo: "default.png"},
		},
	}
	create, delta := g.Delta("https://assets", nil)
	if len(create) == 0 || len(delta) == 0 {
		t.Fatalf("expected create and delta updates for initial delta")
	}
	if g.Teams[0].ID != 1 {
		t.Fatalf("expected teams sorted by ID")
	}
	if g.Teams[0].Logo != "/image/team.png" {
		t.Fatalf("expected default logo replacement, got %q", g.Teams[0].Logo)
	}
	if g.Teams[1].Logo != "https://assets/logo-b.png" {
		t.Fatalf("expected assets prefix logo, got %q", g.Teams[1].Logo)
	}

	old := g
	_, delta2 := g.Delta("https://assets", &old)
	if len(delta2) != 0 {
		t.Fatalf("expected no delta on equivalent game state, got %d", len(delta2))
	}
}
