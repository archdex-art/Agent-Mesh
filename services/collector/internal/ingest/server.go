package ingest

import (
	"context"
	"fmt"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/span"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// apiKeyMetadataKey is the gRPC metadata header carrying the caller's raw
// API key, per docs/otlp-mapping.md's "Authentication" section: the key
// travels as metadata, never as an OTLP span attribute, so it can never
// leak into stored trace data.
const apiKeyMetadataKey = "x-agentmesh-api-key"

// SpanWriter is the persistence dependency Server writes decoded spans to.
// It is the minimal interface the writer package's *Writer satisfies,
// declared here (not imported as a concrete type) so Server stays
// independently testable against a fake (Phase 3's "independently
// testable" standard) without pulling in a ClickHouse dependency for a
// server-wiring unit test.
type SpanWriter interface {
	WriteBatch(ctx context.Context, spans []span.Span) error
}

// Server implements OTLP's TraceServiceServer, wiring together API-key
// authentication (authkeys.Authenticate), OTLP decoding (Decoder), and
// persistence (SpanWriter) — the Collector's complete ingestion path per
// Architecture.md §2.
type Server struct {
	collectorpb.UnimplementedTraceServiceServer

	authStore authkeys.Store
	decoder   *Decoder
	offloader *Offloader
	writer    SpanWriter
}

// NewServer returns a ready-to-use Server.
func NewServer(authStore authkeys.Store, decoder *Decoder, offloader *Offloader, writer SpanWriter) *Server {
	return &Server{authStore: authStore, decoder: decoder, offloader: offloader, writer: writer}
}

// Export implements TraceServiceServer.Export: authenticate the caller,
// decode every span against the authenticated ProjectID, persist the
// successfully-decoded spans, and report OTLP partial-success semantics for
// any spans that failed to decode — a batch is never rejected wholesale for
// one malformed span (docs/otlp-mapping.md, DecodeResourceSpans).
func (s *Server) Export(ctx context.Context, req *collectorpb.ExportTraceServiceRequest) (*collectorpb.ExportTraceServiceResponse, error) {
	rawKey, err := apiKeyFromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	record, err := authkeys.Authenticate(ctx, s.authStore, rawKey)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid API key")
	}
	if record.Role != authkeys.RoleIngest && record.Role != authkeys.RoleAdmin {
		return nil, status.Error(codes.PermissionDenied, "API key does not have ingest permission")
	}

	decoded, decodeErrs := s.decoder.DecodeResourceSpans(req.GetResourceSpans(), record.ProjectID)

	// Per docs/otlp-mapping.md's corrected "Payload size threshold": the
	// Collector, not the exporter, decides whether a payload is stored
	// inline or offloaded to blob storage. This runs after decode
	// (Validate has already confirmed each span is otherwise well-formed)
	// and before persistence, so a partially-offloaded span is never
	// written to ClickHouse.
	for i := range decoded {
		if err := s.offloader.Offload(ctx, &decoded[i]); err != nil {
			return nil, status.Error(codes.Internal, "failed to offload oversized payload to blob storage")
		}
	}

	if err := s.writer.WriteBatch(ctx, decoded); err != nil {
		if amerrors.IsRetryable(err) {
			// Retryable write failures surface as gRPC Unavailable so a
			// well-behaved OTLP exporter's own retry/backoff logic engages
			// (Architecture.md §17's ingestion-path philosophy).
			return nil, status.Error(codes.Unavailable, "trace store temporarily unavailable")
		}
		return nil, status.Error(codes.Internal, "failed to persist spans")
	}

	resp := &collectorpb.ExportTraceServiceResponse{}
	if len(decodeErrs) > 0 {
		resp.PartialSuccess = &collectorpb.ExportTracePartialSuccess{
			RejectedSpans: int64(len(decodeErrs)),
			ErrorMessage:  summarizeDecodeErrors(decodeErrs),
		}
	}
	return resp, nil
}

func apiKeyFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errMissingAPIKey
	}
	values := md.Get(apiKeyMetadataKey)
	if len(values) == 0 || values[0] == "" {
		return "", errMissingAPIKey
	}
	return values[0], nil
}

var errMissingAPIKey = amerrors.New(amerrors.CodeUnauthenticated, "missing "+apiKeyMetadataKey+" metadata")

func summarizeDecodeErrors(errs []error) string {
	if len(errs) == 1 {
		return errs[0].Error()
	}
	// Multiple errors: report the count plus the first error as a
	// representative sample rather than concatenating an unbounded string
	// into the OTLP response.
	return fmt.Sprintf("%d spans rejected; first error: %v", len(errs), errs[0])
}
