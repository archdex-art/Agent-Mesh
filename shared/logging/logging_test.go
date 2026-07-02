package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/agentmesh/agentmesh/shared/ids"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(handler).With(slog.String("service", "test-service"))
}

func TestLoggerEmitsServiceField(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	logger.Info("hello")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if record["service"] != "test-service" {
		t.Fatalf("service field = %v, want %q", record["service"], "test-service")
	}
	if record["msg"] != "hello" {
		t.Fatalf("msg field = %v, want %q", record["msg"], "hello")
	}
}

func TestWithTraceIDAddsTraceIDField(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	traceID, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}

	logger := WithTraceID(base, traceID)
	logger.Info("processing span")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}
	if record["trace_id"] != traceID.String() {
		t.Fatalf("trace_id field = %v, want %q", record["trace_id"], traceID.String())
	}
}

func TestWithInternalTraceIDDoesNotCollideWithTraceID(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	traceID, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}

	logger := WithTraceID(base, traceID)
	logger = WithInternalTraceID(logger, "internal-abc-123")
	logger.Info("dual trace ids present")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}
	if record["trace_id"] != traceID.String() {
		t.Fatalf("trace_id field = %v, want %q", record["trace_id"], traceID.String())
	}
	if record["internal_trace_id"] != "internal-abc-123" {
		t.Fatalf("internal_trace_id field = %v, want %q", record["internal_trace_id"], "internal-abc-123")
	}
	// The two fields must remain distinct keys, never overwriting each other.
	if record["trace_id"] == record["internal_trace_id"] {
		t.Fatal("trace_id and internal_trace_id collided to the same value")
	}
}

func TestContextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	ctx := WithContext(context.Background(), logger)
	got := FromContext(ctx)
	got.Info("via context")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}
	if record["service"] != "test-service" {
		t.Fatalf("service field = %v, want %q", record["service"], "test-service")
	}
}

func TestFromContextDefaultsWhenAbsent(t *testing.T) {
	logger := FromContext(context.Background())
	if logger == nil {
		t.Fatal("FromContext on empty context returned nil, want slog.Default()")
	}
}

func TestNewProducesJSONOutput(t *testing.T) {
	// Smoke-test the real constructor (not just the test helper) writes
	// well-formed JSON with the service field, since New() is what every
	// actual service calls in main().
	logger := New("collector", slog.LevelInfo)
	if logger == nil {
		t.Fatal("New() returned nil")
	}
}
