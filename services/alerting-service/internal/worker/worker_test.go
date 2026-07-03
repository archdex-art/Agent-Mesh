package worker

import (
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

type MockHTTPClient struct {
	mock.Mock
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	var resp *http.Response
	if args.Get(0) != nil {
		resp = args.Get(0).(*http.Response)
	}
	return resp, args.Error(1)
}

func TestWorker_processJob(t *testing.T) {
	mockClient := new(MockHTTPClient)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := &Worker{
		httpClient: mockClient,
		logger:     logger,
	}

	ctx := context.Background()

	t.Run("slack channel - success", func(t *testing.T) {
		mockClient.ExpectedCalls = nil
		reqMatcher := mock.MatchedBy(func(req *http.Request) bool {
			return req.URL.String() == "https://hooks.slack.com/services/test"
		})

		mockResp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
		}
		mockClient.On("Do", reqMatcher).Return(mockResp, nil)

		config := []byte(`{"type": "slack", "webhook_url": "https://hooks.slack.com/services/test"}`)
		err := w.processJob(ctx, "Alert! High cost", config)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("slack channel - http error", func(t *testing.T) {
		mockClient.ExpectedCalls = nil
		reqMatcher := mock.MatchedBy(func(req *http.Request) bool {
			return req.URL.String() == "https://hooks.slack.com/services/test"
		})

		mockClient.On("Do", reqMatcher).Return((*http.Response)(nil), errors.New("network timeout"))

		config := []byte(`{"type": "slack", "webhook_url": "https://hooks.slack.com/services/test"}`)
		err := w.processJob(ctx, "Alert! High cost", config)
		assert.ErrorContains(t, err, "http post: network timeout")
		mockClient.AssertExpectations(t)
	})

	t.Run("slack channel - bad status code", func(t *testing.T) {
		mockClient.ExpectedCalls = nil
		reqMatcher := mock.MatchedBy(func(req *http.Request) bool {
			return req.URL.String() == "https://hooks.slack.com/services/test"
		})

		mockResp := &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(bytes.NewReader([]byte("error"))),
		}
		mockClient.On("Do", reqMatcher).Return(mockResp, nil)

		config := []byte(`{"type": "slack", "webhook_url": "https://hooks.slack.com/services/test"}`)
		err := w.processJob(ctx, "Alert! High cost", config)
		assert.ErrorContains(t, err, "slack API returned non-OK status: 500")
		mockClient.AssertExpectations(t)
	})

	t.Run("unsupported channel type", func(t *testing.T) {
		mockClient.ExpectedCalls = nil

		config := []byte(`{"type": "email"}`)
		err := w.processJob(ctx, "Alert! High cost", config)
		assert.ErrorContains(t, err, "unsupported channel type: email")
		mockClient.AssertExpectations(t)
	})

	t.Run("invalid config JSON", func(t *testing.T) {
		mockClient.ExpectedCalls = nil

		config := []byte(`{invalid`)
		err := w.processJob(ctx, "Alert! High cost", config)
		assert.ErrorContains(t, err, "invalid channel config JSON")
		mockClient.AssertExpectations(t)
	})
}
