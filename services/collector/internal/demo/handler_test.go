package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// defaultScenarioSpanCount and loopScenarioSpanCount are the exact per-trace
// span counts scenario.go's builder.happyPath and builder.loop currently
// produce (root + 4 children, and root + 4 identical tool calls + 1 error
// call, respectively). Hardcoded rather than derived by calling Generate
// in the test itself, so a regression in the builder's shape is actually
// caught instead of the test re-deriving and agreeing with whatever the
// builder happens to produce.
const (
	defaultScenarioSpanCount = 5
	loopScenarioSpanCount    = 6
)

const (
	rawIngestKey  = "am_live_ingestsecret1234567890"
	rawReadKey    = "am_live_readsecret1234567890ab"
	rawUnknownKey = "am_live_unknownsecret1234567890"
)

// fakeAuthStore mirrors shared/authkeys/authkeys_test.go's fakeStore: an
// in-memory, hash-keyed Store so Authenticate's real hashing/lookup logic
// runs against test data without a live Postgres.
type fakeAuthStore struct {
	byHash map[string]authkeys.Record
}

func (f *fakeAuthStore) LookupByHash(ctx context.Context, hashedKey string) (authkeys.Record, error) {
	rec, ok := f.byHash[hashedKey]
	if !ok {
		return authkeys.Record{}, amerrors.New(amerrors.CodeNotFound, "not found")
	}
	return rec, nil
}

// newTestStore registers rawIngestKey (RoleIngest) and rawReadKey
// (RoleRead) for projectID; rawUnknownKey is deliberately never
// registered, for the "unknown API key" case.
func newTestStore(projectID ids.ProjectID) *fakeAuthStore {
	return &fakeAuthStore{byHash: map[string]authkeys.Record{
		authkeys.Hash(rawIngestKey): {ProjectID: projectID, Role: authkeys.RoleIngest},
		authkeys.Hash(rawReadKey):   {ProjectID: projectID, Role: authkeys.RoleRead},
	}}
}

// fakeSpanWriter satisfies SpanWriter without a live ClickHouse writer,
// recording every batch it receives (and how many times it was called) so
// tests can assert on both.
type fakeSpanWriter struct {
	calls     int
	lastBatch []span.Span
	err       error
}

func (f *fakeSpanWriter) WriteBatch(ctx context.Context, spans []span.Span) error {
	f.calls++
	f.lastBatch = spans
	if f.err != nil {
		return f.err
	}
	return nil
}

// fakeSpanPublisher satisfies SpanPublisher without a live Redis
// connection, recording every batch it receives.
type fakeSpanPublisher struct {
	calls     int
	lastBatch []span.Span
}

func (f *fakeSpanPublisher) PublishBatch(ctx context.Context, spans []span.Span) {
	f.calls++
	f.lastBatch = spans
}

func bodyReader(body string) io.Reader {
	if body == "" {
		return nil
	}
	return strings.NewReader(body)
}

// TestHandlerServeHTTP is a table-driven sweep over ServeHTTP's
// authentication, authorization, routing, and generate/write/publish
// behavior, using fakes throughout — no Postgres or ClickHouse needed.
func TestHandlerServeHTTP(t *testing.T) {
	projectID := mustProjectID(t)

	type testCase struct {
		name       string
		method     string // defaults to POST
		path       string // defaults to "/v1/demo/seed"
		apiKey     string // "" omits the header entirely
		body       string
		writerErr  error
		wantStatus int
		check      func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher)
	}

	decodeResponse := func(t *testing.T, rec *httptest.ResponseRecorder) seedResponse {
		t.Helper()
		var resp seedResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
		}
		return resp
	}

	cases := []testCase{
		{
			name:       "missing API key header returns 401",
			apiKey:     "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown API key returns 401",
			apiKey:     rawUnknownKey,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "read-only role key returns 403",
			apiKey:     rawReadKey,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "valid ingest key with empty body creates one default trace",
			apiKey:     rawIngestKey,
			body:       "",
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				resp := decodeResponse(t, rec)
				if resp.TracesCreated != 1 {
					t.Fatalf("TracesCreated = %d, want 1", resp.TracesCreated)
				}
				if len(resp.TraceIDs) != 1 {
					t.Fatalf("len(TraceIDs) = %d, want 1", len(resp.TraceIDs))
				}
				if writer.calls != 1 {
					t.Fatalf("writer.WriteBatch called %d times, want 1", writer.calls)
				}
				if len(writer.lastBatch) != defaultScenarioSpanCount {
					t.Fatalf("writer received %d spans, want %d (default scenario, one trace)", len(writer.lastBatch), defaultScenarioSpanCount)
				}
			},
		},
		{
			name:       "loop scenario with count 3 concatenates all spans into one write",
			apiKey:     rawIngestKey,
			body:       `{"scenario":"loop","count":3}`,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				resp := decodeResponse(t, rec)
				if resp.TracesCreated != 3 {
					t.Fatalf("TracesCreated = %d, want 3", resp.TracesCreated)
				}
				if writer.calls != 1 {
					t.Fatalf("writer.WriteBatch called %d times, want 1 (one batch for all 3 traces)", writer.calls)
				}
				wantSpans := 3 * loopScenarioSpanCount
				if len(writer.lastBatch) != wantSpans {
					t.Fatalf("writer received %d spans, want %d (3 loop traces concatenated)", len(writer.lastBatch), wantSpans)
				}
				if len(resp.TraceIDs) != 3 {
					t.Fatalf("len(TraceIDs) = %d, want 3", len(resp.TraceIDs))
				}
				seen := make(map[string]bool, len(resp.TraceIDs))
				for _, id := range resp.TraceIDs {
					seen[id] = true
				}
				if len(seen) != 3 {
					t.Fatalf("TraceIDs = %v, want 3 distinct ids", resp.TraceIDs)
				}
			},
		},
		{
			name:       "count above max clamps to 50",
			apiKey:     rawIngestKey,
			body:       `{"count":999}`,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				resp := decodeResponse(t, rec)
				if resp.TracesCreated != 50 {
					t.Fatalf("TracesCreated = %d, want 50 (clamped from 999)", resp.TracesCreated)
				}
				if len(resp.TraceIDs) != 50 {
					t.Fatalf("len(TraceIDs) = %d, want 50", len(resp.TraceIDs))
				}
			},
		},
		{
			name:       "GET request returns 405",
			method:     http.MethodGet,
			apiKey:     rawIngestKey,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "wrong path returns 404",
			path:       "/v1/demo/not-seed",
			apiKey:     rawIngestKey,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "OPTIONS request returns 204 with CORS headers",
			method:     http.MethodOptions,
			apiKey:     rawIngestKey,
			wantStatus: http.StatusNoContent,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				if rec.Header().Get("Access-Control-Allow-Origin") == "" {
					t.Fatal("Access-Control-Allow-Origin header missing on OPTIONS response")
				}
				if rec.Header().Get("Access-Control-Allow-Methods") == "" {
					t.Fatal("Access-Control-Allow-Methods header missing on OPTIONS response")
				}
				if rec.Header().Get("Access-Control-Allow-Headers") == "" {
					t.Fatal("Access-Control-Allow-Headers header missing on OPTIONS response")
				}
			},
		},
		{
			name:       "writer error returns 503 and skips publish",
			apiKey:     rawIngestKey,
			writerErr:  errors.New("clickhouse down"),
			wantStatus: http.StatusServiceUnavailable,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				if publisher.calls != 0 {
					t.Fatalf("publisher.PublishBatch called %d times after a failed write, want 0", publisher.calls)
				}
			},
		},
		{
			name:       "successful request publishes to a configured publisher",
			apiKey:     rawIngestKey,
			wantStatus: http.StatusCreated,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, writer *fakeSpanWriter, publisher *fakeSpanPublisher) {
				if publisher.calls != 1 {
					t.Fatalf("publisher.PublishBatch called %d times, want 1", publisher.calls)
				}
				if len(publisher.lastBatch) != defaultScenarioSpanCount {
					t.Fatalf("publisher received %d spans, want %d", len(publisher.lastBatch), defaultScenarioSpanCount)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(projectID)
			writer := &fakeSpanWriter{err: tc.writerErr}
			publisher := &fakeSpanPublisher{}
			h := NewHandler(store, writer, publisher)

			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			path := tc.path
			if path == "" {
				path = "/v1/demo/seed"
			}

			req := httptest.NewRequest(method, path, bodyReader(tc.body))
			if tc.apiKey != "" {
				req.Header.Set(apiKeyHeader, tc.apiKey)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.check != nil {
				tc.check(t, rec, writer, publisher)
			}
		})
	}
}

// TestHandlerServeHTTPWithNilPublisherDoesNotPanic checks NewHandler's
// documented contract that publisher may be nil: a successful request must
// still 201 without dereferencing a nil SpanPublisher.
func TestHandlerServeHTTPWithNilPublisherDoesNotPanic(t *testing.T) {
	projectID := mustProjectID(t)
	store := newTestStore(projectID)
	writer := &fakeSpanWriter{}
	h := NewHandler(store, writer, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/demo/seed", nil)
	req.Header.Set(apiKeyHeader, rawIngestKey)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if writer.calls != 1 {
		t.Fatalf("writer.WriteBatch called %d times, want 1", writer.calls)
	}
}
