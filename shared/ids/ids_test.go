package ids

import "testing" 

func TestTraceIDRoundTrip(t *testing.T) {
	id, err := NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}
	if id.IsZero() {
		t.Fatal("generated TraceID must not be zero")
	}
	s := id.String()
	if len(s) != 32 {
		t.Fatalf("String() length = %d, want 32", len(s))
	}
	parsed, err := ParseTraceID(s)
	if err != nil {
		t.Fatalf("ParseTraceID(%q): %v", s, err)
	}
	if parsed != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", parsed, id)
	}
}

func TestTraceIDUniqueness(t *testing.T) {
	seen := make(map[TraceID]bool)
	for i := 0; i < 1000; i++ {
		id, err := NewTraceID()
		if err != nil {
			t.Fatalf("NewTraceID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate TraceID generated: %v", id)
		}
		seen[id] = true
	}
}

func TestParseTraceIDRejectsWrongLength(t *testing.T) {
	cases := []string{"", "abc", "deadbeef", string(make([]byte, 33))}
	for _, c := range cases {
		if _, err := ParseTraceID(c); err == nil {
			t.Errorf("ParseTraceID(%q) succeeded, want error", c)
		}
	}
}

func TestParseTraceIDRejectsInvalidHex(t *testing.T) {
	// 32 chars but not valid hex.
	bad := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	if _, err := ParseTraceID(bad); err == nil {
		t.Errorf("ParseTraceID(%q) succeeded, want error", bad)
	}
}

func TestSpanIDRoundTrip(t *testing.T) {
	id, err := NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	if id.IsZero() {
		t.Fatal("generated SpanID must not be zero")
	}
	s := id.String()
	if len(s) != 16 {
		t.Fatalf("String() length = %d, want 16", len(s))
	}
	parsed, err := ParseSpanID(s)
	if err != nil {
		t.Fatalf("ParseSpanID(%q): %v", s, err)
	}
	if parsed != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", parsed, id)
	}
}

func TestProjectIDIsUUIDv7(t *testing.T) {
	id, err := NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	raw := [16]byte(id)
	version := raw[6] >> 4
	if version != 7 {
		t.Fatalf("version nibble = %d, want 7", version)
	}
	variant := raw[8] >> 6
	if variant != 0b10 {
		t.Fatalf("variant bits = %02b, want 10", variant)
	}
}

func TestProjectIDRoundTrip(t *testing.T) {
	id, err := NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	s := id.String()
	parsed, err := ParseProjectID(s)
	if err != nil {
		t.Fatalf("ParseProjectID(%q): %v", s, err)
	}
	if parsed != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", parsed, id)
	}
}

func TestProjectIDMonotonicOrdering(t *testing.T) {
	// UUIDv7's leading 48 bits are a millisecond timestamp, so IDs generated
	// in sequence should sort lexicographically by their string form for the
	// timestamp portion (System Design.md §2.2 relies on this for B-tree
	// index locality). We can't guarantee strict ordering within the same
	// millisecond, but the first several hex chars (timestamp) must be
	// non-decreasing across a small sleep-free burst is not guaranteed either;
	// what we *can* assert deterministically is that two IDs minted apart in
	// time have non-decreasing timestamp prefixes.
	first, err := NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	second, err := NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	firstRaw, secondRaw := [16]byte(first), [16]byte(second)
	firstMs := uint64(firstRaw[0])<<40 | uint64(firstRaw[1])<<32 | uint64(firstRaw[2])<<24 | uint64(firstRaw[3])<<16 | uint64(firstRaw[4])<<8 | uint64(firstRaw[5])
	secondMs := uint64(secondRaw[0])<<40 | uint64(secondRaw[1])<<32 | uint64(secondRaw[2])<<24 | uint64(secondRaw[3])<<16 | uint64(secondRaw[4])<<8 | uint64(secondRaw[5])
	if secondMs < firstMs {
		t.Fatalf("second ID's timestamp (%d) precedes first's (%d)", secondMs, firstMs)
	}
}

func TestReplayIDRoundTrip(t *testing.T) {
	id, err := NewReplayID()
	if err != nil {
		t.Fatalf("NewReplayID: %v", err)
	}
	parsed, err := ParseReplayID(id.String())
	if err != nil {
		t.Fatalf("ParseReplayID(%q): %v", id.String(), err)
	}
	if parsed != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", parsed, id)
	}
}
