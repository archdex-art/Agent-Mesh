package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockInterceptor struct {
	err error
}

func (m *mockInterceptor) Intercept(ctx context.Context, req MCPRPCRequest) error {
	return m.err
}

func TestProxyForwardsValidRequest(t *testing.T) {
	// Dummy upstream server
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc": "2.0", "result": "ok", "id": 1}`))
	}))
	defer upstream.Close()

	gw, err := NewGateway(upstream.URL, &mockInterceptor{err: nil})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	reqBody := `{"jsonrpc": "2.0", "method": "ping", "id": 1}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if !upstreamHit {
		t.Fatal("Upstream server was not hit")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}
}

func TestProxyBlocksRequestAndReturnsJSONRPCError(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	gw, err := NewGateway(upstream.URL, &mockInterceptor{err: errors.New("malicious payload")})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	reqBody := `{"jsonrpc": "2.0", "method": "execute", "id": 42}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	gw.ServeHTTP(rec, req)

	if upstreamHit {
		t.Fatal("Upstream server was hit despite interceptor error")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected HTTP status 200 for JSON-RPC error, got %d", rec.Code)
	}

	var resp MCPRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("Expected JSON-RPC error object, got nil")
	}
	if resp.Error.Code != -32000 {
		t.Fatalf("Expected error code -32000, got %d", resp.Error.Code)
	}
	if resp.Error.Message != "Blocked by Guardrail: malicious payload" {
		t.Fatalf("Unexpected error message: %s", resp.Error.Message)
	}
	if string(resp.ID) != "42" {
		t.Fatalf("Expected ID 42, got %s", string(resp.ID))
	}
}
