"""OTLP export path: batches Span objects and sends them to the Collector.

Implements docs/otlp-mapping.md's attribute contract exactly, mirroring the
Go Collector's internal/ingest/decode.go so the two sides agree on every
attribute key.

System Design.md §3 requires the SDK to batch spans rather than emit one
network call per span ("System Design.md §3 requires batching on the
Collector side ... as a deliberate, non-negotiable design constraint" —
that requirement starts on the SDK side, since a batch of one is exactly
what an un-batched exporter would produce). BatchingExporter below
buffers spans and flushes on a size or time threshold, never per-span.
"""

from __future__ import annotations

import logging
import threading
import time
from typing import Iterable

import grpc
from opentelemetry.proto.collector.trace.v1 import trace_service_pb2, trace_service_pb2_grpc
from opentelemetry.proto.common.v1 import common_pb2
from opentelemetry.proto.trace.v1 import trace_pb2

from ._span import Span, CURRENT_SCHEMA_VERSION

logger = logging.getLogger("agentmesh.exporter")

# Metadata key carrying the caller's raw API key, matching
# services/collector/internal/ingest/server.go's apiKeyMetadataKey exactly
# (docs/otlp-mapping.md's "Authentication" section).
_API_KEY_METADATA_KEY = "x-agentmesh-api-key"

_DEFAULT_BATCH_SIZE = 50
_DEFAULT_FLUSH_INTERVAL_SECONDS = 1.0


def _string_value(value: str) -> common_pb2.AnyValue:
    return common_pb2.AnyValue(string_value=value)


def _int_value(value: int) -> common_pb2.AnyValue:
    return common_pb2.AnyValue(int_value=value)


def _double_value(value: float) -> common_pb2.AnyValue:
    return common_pb2.AnyValue(double_value=value)


def _kv(key: str, value: common_pb2.AnyValue) -> common_pb2.KeyValue:
    return common_pb2.KeyValue(key=key, value=value)


def encode_span(span: Span) -> trace_pb2.Span:
    """Encode a Span into an OTLP trace_pb2.Span per docs/otlp-mapping.md.

    This is the Python-side mirror of
    services/collector/internal/ingest/decode.go's DecodeSpan — every
    attribute key here must have a corresponding case in that file's
    attribute-reading logic, and vice versa.
    """
    attributes = [
        _kv("agentmesh.schema_version", _int_value(CURRENT_SCHEMA_VERSION)),
        _kv("agentmesh.project_id", _string_value(span.project_id)),
        _kv("agentmesh.span_kind", _string_value(span.kind.value)),
    ]
    if span.status is not None:
        attributes.append(_kv("agentmesh.status", _string_value(span.status.value)))
    if span.input is not None:
        attributes.append(_kv("agentmesh.input.inline", _string_value(span.input)))
    if span.output is not None:
        attributes.append(_kv("agentmesh.output.inline", _string_value(span.output)))
    if span.token_input is not None:
        attributes.append(_kv("agentmesh.token.input", _int_value(span.token_input)))
    if span.token_output is not None:
        attributes.append(_kv("agentmesh.token.output", _int_value(span.token_output)))
    if span.cost_usd is not None:
        # docs/otlp-mapping.md: cost_usd must be omitted entirely when
        # unknown, never sent as 0.0 — enforced here by the `is not None`
        # check, matching shared/span.Span's nil-means-unknown invariant.
        attributes.append(_kv("agentmesh.cost_usd", _double_value(span.cost_usd)))
    for key, value in span.attributes.items():
        attributes.append(_kv(key, _string_value(value)))

    otlp_span = trace_pb2.Span(
        trace_id=bytes.fromhex(span.trace_id),
        span_id=bytes.fromhex(span.span_id),
        name=span.name,
        start_time_unix_nano=span.start_time_ns,
        attributes=attributes,
    )
    if span.parent_span_id:
        otlp_span.parent_span_id = bytes.fromhex(span.parent_span_id)
    if span.end_time_ns is not None:
        otlp_span.end_time_unix_nano = span.end_time_ns
    return otlp_span


def build_export_request(spans: Iterable[Span]) -> trace_service_pb2.ExportTraceServiceRequest:
    """Wrap a batch of spans into a single OTLP export request."""
    otlp_spans = [encode_span(s) for s in spans]
    return trace_service_pb2.ExportTraceServiceRequest(
        resource_spans=[
            trace_pb2.ResourceSpans(
                scope_spans=[trace_pb2.ScopeSpans(spans=otlp_spans)]
            )
        ]
    )


class BatchingExporter:
    """Buffers spans and flushes them to the Collector on a size or time
    threshold, never one network call per span (System Design.md §3).

    A background thread owns the time-based flush; `record()` triggers an
    immediate flush when the buffer reaches `batch_size`, matching the
    "flush periodically... or on batch size" pattern documented for the
    Collector's own ClickHouse writer.
    """

    def __init__(
        self,
        endpoint: str,
        api_key: str,
        *,
        batch_size: int = _DEFAULT_BATCH_SIZE,
        flush_interval_seconds: float = _DEFAULT_FLUSH_INTERVAL_SECONDS,
        insecure: bool = True,
    ) -> None:
        self._api_key = api_key
        self._batch_size = batch_size
        self._flush_interval_seconds = flush_interval_seconds
        self._channel = (
            grpc.insecure_channel(endpoint) if insecure else grpc.secure_channel(endpoint, grpc.ssl_channel_credentials())
        )
        self._stub = trace_service_pb2_grpc.TraceServiceStub(self._channel)

        self._lock = threading.Lock()
        self._buffer: list[Span] = []
        self._stop_event = threading.Event()
        self._flush_thread = threading.Thread(target=self._flush_loop, daemon=True)
        self._flush_thread.start()

    def record(self, span: Span) -> None:
        """Buffer a completed span for export. Never raises on a transient
        export failure — Architecture.md §17's ingestion-path philosophy
        ("a Collector outage degrades to traces delayed, never to agent
        crashes") applies to the SDK's own recording call, which must never
        be the reason a customer's agent crashes."""
        should_flush = False
        with self._lock:
            self._buffer.append(span)
            if len(self._buffer) >= self._batch_size:
                should_flush = True
        if should_flush:
            self.flush()

    def flush(self) -> None:
        """Send every currently-buffered span immediately."""
        with self._lock:
            if not self._buffer:
                return
            batch, self._buffer = self._buffer, []

        request = build_export_request(batch)
        metadata = ((_API_KEY_METADATA_KEY, self._api_key),)
        try:
            self._stub.Export(request, metadata=metadata, timeout=10.0)
        except grpc.RpcError as exc:
            # Per Architecture.md §17: never raise into the customer's agent
            # code. A dropped batch is logged, not fatal. (Local buffering
            # with retry/backoff, per the same section, is a documented
            # post-MVP hardening step once real-world drop rates are
            # measured — not implemented here to avoid speculative
            # complexity ahead of that data.)
            logger.warning("agentmesh: failed to export %d span(s): %s", len(batch), exc)

    def shutdown(self, timeout: float = 5.0) -> None:
        """Stop the background flush thread and flush any remaining spans."""
        self._stop_event.set()
        self._flush_thread.join(timeout=timeout)
        self.flush()
        self._channel.close()

    def _flush_loop(self) -> None:
        while not self._stop_event.wait(self._flush_interval_seconds):
            self.flush()
