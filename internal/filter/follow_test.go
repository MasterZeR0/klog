package filter

import "testing"

func TestIDSet(t *testing.T) {
	s := NewIDSet("traceId")
	if s.Has(jl(`{"traceId":"t1"}`)) {
		t.Fatal("empty set has t1")
	}
	if !s.Add(jl(`{"traceId":"t1","msg":"seed"}`)) {
		t.Fatal("Add of a line with the field should report true")
	}
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"traceId":"t1"}`, true},
		{`{"traceId":"t2"}`, false},
		{`{"msg":"no id"}`, false},
		{`plain t1`, false},
		{`{"traceId":null}`, false},
		{`{"traceId":{"a":1}}`, false},
	}
	for _, tc := range cases {
		if got := s.Has(jl(tc.raw)); got != tc.want {
			t.Errorf("Has(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestIDSetAddIgnoresLinesWithoutUsableID(t *testing.T) {
	s := NewIDSet("id")
	for _, raw := range []string{`{"msg":"x"}`, `plain`, `{"id":null}`, `{"id":[1]}`} {
		if s.Add(jl(raw)) {
			t.Errorf("Add(%s) = true", raw)
		}
	}
}

func TestIDSetMatchesNumbersAndBools(t *testing.T) {
	s := NewIDSet("id")
	s.Add(jl(`{"id":12345678901234567890}`))
	s.Add(jl(`{"id":true}`))
	if !s.Has(jl(`{"id":12345678901234567890}`)) || !s.Has(jl(`{"id":true}`)) || s.Has(jl(`{"id":"12345678901234567891"}`)) {
		t.Fatal("number/bool ids mismatched")
	}
}

func TestIDSetKeyIsTopLevelOnly(t *testing.T) {
	s := NewIDSet("req.id")
	s.Add(jl(`{"req":{"id":"x"},"req.id":"y"}`))
	if !s.Has(jl(`{"req.id":"y"}`)) || s.Has(jl(`{"req":{"id":"x"}}`)) {
		t.Fatal("key should be a literal top-level key")
	}
}

func TestIDSetEvictsOldestWhenFull(t *testing.T) {
	s := newIDSetCap("id", 3)
	for _, id := range []string{"a", "b", "c"} {
		s.Add(jl(`{"id":"` + id + `"}`))
	}
	s.Add(jl(`{"id":"b"}`)) // re-adding does not refresh or grow
	s.Add(jl(`{"id":"d"}`)) // evicts a, the oldest inserted
	s.Add(jl(`{"id":"e"}`)) // evicts b
	want := map[string]bool{"a": false, "b": false, "c": true, "d": true, "e": true}
	for id, w := range want {
		if got := s.Has(jl(`{"id":"` + id + `"}`)); got != w {
			t.Errorf("Has(%s) = %v, want %v", id, got, w)
		}
	}
	if len(s.ids) != 3 || len(s.ring) != 3 {
		t.Fatalf("set grew past its cap: %d ids, ring %d", len(s.ids), len(s.ring))
	}
}

func TestNewIDSetDefaultCap(t *testing.T) {
	if NewIDSet("id").cap != 100000 {
		t.Fatal("default cap should be 100000")
	}
}
