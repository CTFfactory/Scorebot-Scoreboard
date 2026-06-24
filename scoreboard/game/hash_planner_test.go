package game

import "testing"

type unknownHashType struct{}
type localStringer struct{}

func (localStringer) String() string { return "local-stringer" }

func containsUpdateID(items []update, id string) bool {
	for i := range items {
		if items[i].ID == id {
			return true
		}
	}
	return false
}

func TestHasherSupportedAndUnsupportedTypes(t *testing.T) {
	h := new(hasher)
	if err := h.Hash(true); err != nil {
		t.Fatalf("hash bool: %v", err)
	}
	if err := h.Hash("abc"); err != nil {
		t.Fatalf("hash string: %v", err)
	}
	if err := h.Hash([]byte{1, 2, 3}); err != nil {
		t.Fatalf("hash bytes: %v", err)
	}
	if err := h.Hash(int64(42)); err != nil {
		t.Fatalf("hash int64: %v", err)
	}
	if err := h.Hash(uint32(7)); err != nil {
		t.Fatalf("hash uint32: %v", err)
	}
	if err := h.Hash(float64(3.14)); err != nil {
		t.Fatalf("hash float64: %v", err)
	}
	if err := h.Hash(float32(1.5)); err != nil {
		t.Fatalf("hash float32: %v", err)
	}
	if err := h.Hash(int8(-3)); err != nil {
		t.Fatalf("hash int8: %v", err)
	}
	if err := h.Hash(uint8(3)); err != nil {
		t.Fatalf("hash uint8: %v", err)
	}
	if err := h.Hash(int16(-7)); err != nil {
		t.Fatalf("hash int16: %v", err)
	}
	if err := h.Hash(uint16(7)); err != nil {
		t.Fatalf("hash uint16: %v", err)
	}
	if err := h.Hash(int32(-11)); err != nil {
		t.Fatalf("hash int32: %v", err)
	}
	if err := h.Hash(int(13)); err != nil {
		t.Fatalf("hash int: %v", err)
	}
	if err := h.Hash(uint(13)); err != nil {
		t.Fatalf("hash uint: %v", err)
	}
	if err := h.Hash(localStringer{}); err != nil {
		t.Fatalf("hash stringer: %v", err)
	}
	if h.Sum64() == 0 {
		t.Fatalf("expected non-zero running hash")
	}
	if seg := h.Segment(); seg == 0 {
		t.Fatalf("expected non-zero segment")
	}
	h.Reset()
	if h.Sum64() != fnvStart {
		t.Fatalf("expected reset sum %d, got %d", uint64(fnvStart), h.Sum64())
	}
	if err := h.Hash(unknownHashType{}); err == nil {
		t.Fatalf("expected unsupported type error")
	}
}

func TestUpdateFnvDeterministic(t *testing.T) {
	in := []byte("same-input")
	a := updateFnv(fnvStart, in)
	b := updateFnv(fnvStart, in)
	if a != b {
		t.Fatalf("expected deterministic fnv values, got %d and %d", a, b)
	}
}

func TestPlannerAndCompareHelpers(t *testing.T) {
	var (
		p planner
		c = make(compare)
	)
	c.One(tweet{ID: 1})
	c.Two(tweet{ID: 1})
	c.Two(tweet{ID: 2})

	if !c[1].First() || !c[1].Second() {
		t.Fatalf("expected compare pair for id 1")
	}
	if c[2].First() || !c[2].Second() {
		t.Fatalf("expected second-only compare pair for id 2")
	}

	p.Prefix("root")
	p.Value("k", "v", "cls")
	p.Property("k2", "v2", "style")
	p.DeltaValue("k3", "v3", "cls3")
	p.DeltaProperty("k4", "v4", "style4")
	p.Remove("gone")
	p.Event(1, 2, map[string]string{"a": "b"})
	p.DeltaEvent(2, 3, map[string]string{"c": "d"})
	p.RemoveEvent(3, 4)
	p.rollbackPrefix()

	if len(p.Create) == 0 {
		t.Fatalf("expected create updates")
	}
	if len(p.Delta) == 0 {
		t.Fatalf("expected delta updates")
	}
	if p.prefix != "" {
		t.Fatalf("expected prefix rollback to empty")
	}
	if !containsUpdateID(p.Delta, "root-gone") {
		t.Fatalf("expected remove update id root-gone")
	}
}

func TestPlannerIDConstructionBranches(t *testing.T) {
	var p planner
	p.Remove("")
	if !containsUpdateID(p.Delta, "") {
		t.Fatalf("expected empty id removal when no prefix is set")
	}

	p = planner{prefix: "pref"}
	p.Remove("")
	if !containsUpdateID(p.Delta, "pref") {
		t.Fatalf("expected prefix-only remove id when item id is empty")
	}

	p = planner{}
	p.Value("id", "v", "cls")
	if !containsUpdateID(p.Create, "id") {
		t.Fatalf("expected non-prefixed value id")
	}
	p = planner{}
	p.Value("", "v", "cls")
	if !containsUpdateID(p.Create, "") {
		t.Fatalf("expected empty value id when no prefix is set")
	}

	p = planner{prefix: "root"}
	p.DeltaValue("k", "v", "c")
	if !containsUpdateID(p.Delta, "root-k") {
		t.Fatalf("expected prefixed delta value id")
	}
	p = planner{prefix: "root"}
	p.DeltaValue("", "v", "c")
	if !containsUpdateID(p.Delta, "root") {
		t.Fatalf("expected prefix-only delta value id when key is empty")
	}
}

func TestPrintStrConversions(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{[]byte("x"), "x"},
		{"str", "str"},
		{int(-1), "-1"},
		{uint(1), "1"},
		{int8(-2), "-2"},
		{uint8(2), "2"},
		{int16(-3), "-3"},
		{uint16(3), "3"},
		{int32(-4), "-4"},
		{uint32(4), "4"},
		{int64(-5), "-5"},
		{uint64(5), "5"},
		{float32(1.5), "1.50"},
		{float64(2.5), "2.50"},
	}
	for _, tc := range cases {
		got := printStr(tc.in)
		if got != tc.want {
			t.Fatalf("expected %q got %q", tc.want, got)
		}
	}
	if got := printStr(struct{}{}); got == "" {
		t.Fatalf("expected non-empty fmt-based fallback formatting, got %q", got)
	}
}
