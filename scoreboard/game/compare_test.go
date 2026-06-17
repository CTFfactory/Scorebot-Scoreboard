package game

import "testing"

func TestScoreComparisons(t *testing.T) {
	var h hasher

	s1 := score{Total: 10, Health: 20}
	_ = s1.Hash(&h)
	s2 := score{Total: 10, Health: 20}
	_ = s2.Hash(&h)
	p := new(planner)
	s1.Compare(p, s2)
	if len(p.Delta) != 0 || len(p.Create) != 2 {
		t.Fatalf("expected create-only updates for equal scores")
	}

	p = new(planner)
	s3 := score{Total: 99, Health: 20}
	_ = s3.Hash(&h)
	s3.Compare(p, s1)
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for changed scores")
	}
}

func TestScoreFlagAndTicketComparisons(t *testing.T) {
	var h hasher
	sf := scoreFlag{Open: 1, Lost: 2, Captured: 3}
	_ = sf.Hash(&h)
	p := new(planner)
	sf.Compare(p, scoreFlag{})
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for new score flags")
	}
	// This captures the existing field ID emitted by the current implementation.
	if !containsUpdateID(p.Delta, "score-fpen") {
		t.Fatalf("expected score-fpen update id in current behavior")
	}

	st := scoreTicket{Open: 4, Closed: 5}
	_ = st.Hash(&h)
	p = new(planner)
	st.Compare(p, scoreTicket{})
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for new ticket scores")
	}
}

func TestHostServiceAndBeaconComparison(t *testing.T) {
	var h hasher

	svc := service{ID: 11, Port: 80, State: green, Protocol: tcp, Bonus: true}
	_ = svc.Hash(&h)
	hs := host{
		ID:       10,
		Name:     "host-a",
		Online:   true,
		Services: []service{svc},
	}
	_ = hs.Hash(&h)

	p := new(planner)
	hs.Compare(p, emptyHost)
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for new host")
	}

	old := hs
	p = new(planner)
	hs.Compare(p, old)
	if len(p.Delta) != 0 {
		t.Fatalf("expected no delta updates for equivalent host")
	}

	b := beacon{ID: 1, Team: 2, Color: "#fff"}
	_ = b.Hash(&h)
	p = new(planner)
	b.Compare(p, emptyBeacon)
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates for new beacon")
	}
}

func TestTeamComparison(t *testing.T) {
	var h hasher

	tm := team{
		ID:      5,
		Name:    "Blue",
		Logo:    "/logo.png",
		Color:   "#123",
		Minimal: true,
		Offense: true,
		Hosts: []host{
			{
				ID:       8,
				Name:     "srv",
				Online:   true,
				Services: []service{{ID: 9, Port: 443, State: yellow, Protocol: tcp}},
			},
		},
		Beacons: []beacon{{ID: 7, Team: 5, Color: "#999"}},
		Score:   score{Total: 1, Health: 2},
		Flags:   scoreFlag{Open: 1, Lost: 0, Captured: 0},
		Tickets: scoreTicket{Open: 2, Closed: 1},
	}
	_ = tm.Hash(&h)

	p := new(planner)
	tm.Compare(p, emptyTeam)
	if len(p.Delta) == 0 || len(p.Create) == 0 {
		t.Fatalf("expected team compare to emit updates for empty old team")
	}

	old := tm
	p = new(planner)
	tm.Compare(p, old)
	if len(p.Delta) != 0 {
		t.Fatalf("expected no delta updates for equivalent team")
	}
}

func TestTeamSortHelpers(t *testing.T) {
	tm := team{
		Hosts: []host{
			{Name: "z-host"},
			{Name: "a-host"},
		},
	}
	if !tm.Less(1, 0) {
		t.Fatalf("expected Less to compare host names lexicographically")
	}
	tm.Swap(0, 1)
	if tm.Hosts[0].Name != "a-host" {
		t.Fatalf("expected Swap to exchange host positions")
	}
}
