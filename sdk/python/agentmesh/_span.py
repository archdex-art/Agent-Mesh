"""AgentMesh SDK's span domain model.

Mirrors the shape defined in shared/span/span.go and docs/otlp-mapping.md.
This is a deliberate parallel implementation, not a shared import — the Go
`shared/span` package and this Python module are separate language
ecosystems that agree only through the wire contract in
docs/otlp-mapping.md, exactly as that document states: "This document is
the wire contract between every OTLP exporter ... and the Collector's
decoder." Any change to the span model's meaning must be made in both
places and reflected in that document.
"""

from __future__ import annotations

import enum
import os
import time
from dataclasses import dataclass, field

# Mirrors shared/span.CurrentSchemaVersion. Any wire-format change bumps
# both this value and the Go constant simultaneously (docs/otlp-mapping.md).
CURRENT_SCHEMA_VERSION = 1


class SpanKind(str, enum.Enum):
    """Mirrors shared/span.Kind (Architecture.md §3)."""

    LLM_CALL = "llm.call"
    TOOL_CALL = "tool.call"
    AGENT_HANDOFF = "agent.handoff"
    MCP_CALL = "mcp.call"


class SpanStatus(str, enum.Enum):
    """Mirrors shared/span.Status."""

    OK = "ok"
    ERROR = "error"
    TIMEOUT = "timeout"
    DENIED = "denied"


# Per docs/otlp-mapping.md's "Payload size threshold" section (corrected):
# the 4KB inline/blob-ref decision is made by the Collector on ingestion,
# not by the exporter. The SDK always sends the full payload inline and
# never truncates or uploads to blob storage itself — doing so would
# require distributing object-storage write credentials to every
# customer's agent process, which Architecture.md §2 explicitly rules out
# ("[the SDK] never sees the Trace Store directly").


def _new_trace_id() -> str:
    """Generate a 32-hex-char trace id, matching shared/ids.TraceID's wire
    format (a 16-byte value rendered as lowercase hex)."""
    return os.urandom(16).hex()


def _new_span_id() -> str:
    """Generate a 16-hex-char span id, matching shared/ids.SpanID's wire
    format (an 8-byte value rendered as lowercase hex)."""
    return os.urandom(8).hex()


@dataclass
class Span:
    """A single unit of work inside a trace, prior to OTLP encoding.

    Mirrors shared/span.Span's fields that are populated client-side; the
    Collector-computed fields (schema validation results, ingested_at,
    blob_ref) have no client-side representation — the SDK never decides
    where a payload is stored, only what it is (docs/otlp-mapping.md's
    corrected "Payload size threshold" section).
    """

    project_id: str
    kind: SpanKind
    name: str
    trace_id: str = field(default_factory=_new_trace_id)
    span_id: str = field(default_factory=_new_span_id)
    parent_span_id: str | None = None
    start_time_ns: int = field(default_factory=time.time_ns)
    end_time_ns: int | None = None
    status: SpanStatus | None = None
    input: str | None = None
    output: str | None = None
    token_input: int | None = None
    token_output: int | None = None
    cost_usd: float | None = None
    attributes: dict[str, str] = field(default_factory=dict)

    def finish(
        self,
        *,
        status: SpanStatus = SpanStatus.OK,
        output: str | None = None,
        token_input: int | None = None,
        token_output: int | None = None,
        cost_usd: float | None = None,
    ) -> None:
        """Mark the span complete. Mirrors setting EndTime/Status/Output on
        the Go side; called once the wrapped LLM/tool call returns."""
        self.end_time_ns = time.time_ns()
        self.status = status
        if output is not None:
            self.output = output
        if token_input is not None:
            self.token_input = token_input
        if token_output is not None:
            self.token_output = token_output
        if cost_usd is not None:
            self.cost_usd = cost_usd

    def set_input(self, input_value: str) -> None:
        self.input = input_value
