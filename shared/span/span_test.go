package span

import (
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
)

func mustSpan(t *testing.T) Span {
	t.Helper()
	projectID, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	traceID, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}
	spanID, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	start := time.Now()
	return Span{
		SchemaVersion: CurrentSchemaVersion,
		ProjectID:     projectID,
		TraceID:       traceID,
		SpanID:        spanID,
		Kind:          KindLLMCall,
		Name:          "gpt-4.1",
		StartTime:     start,
		EndTime:       start.Add(200 * time.Millisecond),
		Status:        StatusOK,
	}
}

func TestValidSpanPasses(t *testing.T) {
	s := mustSpan(t)
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() on well-formed span: %v", err)
	}
}

func TestValidateRejectsMissingProjectID(t *testing.T) {
	s := mustSpan(t)
	s.ProjectID = ids.ProjectID{}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with zero ProjectID, want error")
	}
}

func TestValidateRejectsMissingTraceID(t *testing.T) {
	s := mustSpan(t)
	s.TraceID = ids.TraceID{}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with zero TraceID, want error")
	}
}

func TestValidateRejectsMissingSpanID(t *testing.T) {
	s := mustSpan(t)
	s.SpanID = ids.SpanID{}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with zero SpanID, want error")
	}
}

func TestValidateRejectsUnrecognizedKind(t *testing.T) {
	s := mustSpan(t)
	s.Kind = Kind("bogus.kind")
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with unrecognized kind, want error")
	}
}

func TestValidateAcceptsAllFourKinds(t *testing.T) {
	for k := range ValidKinds {
		s := mustSpan(t)
		s.Kind = k
		if err := s.Validate(); err != nil {
			t.Errorf("Validate() rejected valid kind %q: %v", k, err)
		}
	}
}

func TestValidateRejectsMissingName(t *testing.T) {
	s := mustSpan(t)
	s.Name = ""
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with empty Name, want error")
	}
}

func TestValidateRejectsUnrecognizedStatus(t *testing.T) {
	s := mustSpan(t)
	s.Status = Status("bogus")
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with unrecognized status, want error")
	}
}

func TestValidateAllowsEmptyStatusForInFlightSpan(t *testing.T) {
	// A span that hasn't completed yet (still executing) may have no status.
	s := mustSpan(t)
	s.Status = ""
	s.EndTime = time.Time{}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() rejected in-flight span (empty status/end time): %v", err)
	}
}

func TestValidateRejectsEndBeforeStart(t *testing.T) {
	s := mustSpan(t)
	s.EndTime = s.StartTime.Add(-1 * time.Second)
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with EndTime before StartTime, want error")
	}
}

func TestValidateRejectsBothInlineAndBlobRef(t *testing.T) {
	s := mustSpan(t)
	s.Input = Payload{Inline: "some data", BlobRef: "s3://bucket/key"}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with both Inline and BlobRef set, want error")
	}
}

func TestValidateRejectsMissingSchemaVersion(t *testing.T) {
	s := mustSpan(t)
	s.SchemaVersion = 0
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded with zero SchemaVersion, want error")
	}
}

func TestHasParent(t *testing.T) {
	s := mustSpan(t)
	if s.HasParent() {
		t.Fatal("HasParent() = true for a span with zero ParentSpanID, want false")
	}
	parentID, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	s.ParentSpanID = parentID
	if !s.HasParent() {
		t.Fatal("HasParent() = false for a span with a non-zero ParentSpanID, want true")
	}
}

func TestDuration(t *testing.T) {
	s := mustSpan(t)
	want := 200 * time.Millisecond
	if got := s.Duration(); got != want {
		t.Fatalf("Duration() = %v, want %v", got, want)
	}
}

func TestPayloadIsInlineAndIsEmpty(t *testing.T) {
	cases := []struct {
		name        string
		payload     Payload
		wantInline  bool
		wantIsEmpty bool
	}{
		{"empty", Payload{}, true, true},
		{"inline only", Payload{Inline: "data"}, true, false},
		{"blob only", Payload{BlobRef: "s3://x"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.payload.IsInline(); got != c.wantInline {
				t.Errorf("IsInline() = %v, want %v", got, c.wantInline)
			}
			if got := c.payload.IsEmpty(); got != c.wantIsEmpty {
				t.Errorf("IsEmpty() = %v, want %v", got, c.wantIsEmpty)
			}
		})
	}
}

func TestCostUSDNilMeansUnknownNotZero(t *testing.T) {
	// System Design.md §7: a tool.call span without a configured cost must
	// default to nil (unknown), not 0.0, so cost rollups exclude it rather
	// than understating spend.
	s := mustSpan(t)
	s.Kind = KindToolCall
	s.CostUSD = nil
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() rejected span with nil CostUSD: %v", err)
	}
	if s.CostUSD != nil {
		t.Fatal("expected CostUSD to remain nil")
	}
}
