# AgentMesh OTLP Attribute Mapping (schema_version 1)

This document is the wire contract between every OTLP exporter (the Python
SDK, the TypeScript SDK, the MCP Gateway) and the Collector's decoder. It
exists because OTLP's `Span` message has no native fields for AgentMesh's
domain concepts (span kind, cost, token counts, replayable payloads) —
`Architecture.md` §9 and `Product Requirements.md` §2 commit AgentMesh to
riding OTLP's `attributes` extensibility instead of inventing a custom wire
format, and this document is what keeps every independent implementation of
that mapping in agreement.

**Any change to this file is a breaking change to `schema_version` and must
bump `CurrentSchemaVersion` in `shared/span/span.go` and be applied to every
exporter simultaneously.**

## Direct OTLP field mapping

These come from OTLP's native `Span` message fields, not attributes:

| `span.Span` field | OTLP `Span` field |
|---|---|
| `TraceID` | `trace_id` (16 bytes) |
| `SpanID` | `span_id` (8 bytes) |
| `ParentSpanID` | `parent_span_id` (8 bytes, absent means root) |
| `StartTime` | `start_time_unix_nano` |
| `EndTime` | `end_time_unix_nano` |

## AgentMesh attribute keys

All AgentMesh-specific data rides in `Span.attributes` under the
`agentmesh.*` namespace, as `KeyValue` entries. `Span.name` carries the
model/tool/agent name directly (OTLP's native field already matches
`span.Span.Name`'s meaning, so no attribute is needed for it).

| Attribute key | Type | Required | Meaning |
|---|---|---|---|
| `agentmesh.schema_version` | int | yes | Must equal the schema version this document defines (`1`). The Collector rejects (`CodeSchemaVersionMismatch`) any span whose value it does not recognize. |
| `agentmesh.project_id` | string | yes | UUID string form of the target `ProjectID`. Redundant with the API key's own project scope, and the Collector cross-checks the two match — a caller cannot claim a different project than its API key authorizes. |
| `agentmesh.span_kind` | string | yes | One of `llm.call`, `tool.call`, `agent.handoff`, `mcp.call` (`shared/span.Kind`). |
| `agentmesh.status` | string | no | One of `ok`, `error`, `timeout`, `denied` (`shared/span.Status`). Absent means the span was still in flight when exported. |
| `agentmesh.input.inline` | string | no | Small (<4KB) input payload, mutually exclusive with `agentmesh.input.blob_ref`. |
| `agentmesh.input.blob_ref` | string | no | Object-store key for a large input payload. |
| `agentmesh.output.inline` | string | no | Small (<4KB) output payload, mutually exclusive with `agentmesh.output.blob_ref`. |
| `agentmesh.output.blob_ref` | string | no | Object-store key for a large output payload. |
| `agentmesh.token.input` | int | no | Input token count, applicable to `llm.call` spans. |
| `agentmesh.token.output` | int | no | Output token count, applicable to `llm.call` spans. |
| `agentmesh.cost_usd` | double | no | Cost in USD. **Absent means unknown, never assumed to be `0.0`** (`System Design.md` §7) — exporters must omit this attribute entirely rather than send `0.0` when cost is not computed. |

Any other attribute key is passed through verbatim into
`span.Span.Attributes` as free-form metadata and is never required or
interpreted by the Collector — with the two exceptions below, which the
Collector *also* extracts into dedicated `span.Span` fields (in addition
to leaving them in the passthrough map), because they answer a
first-class query the trace store is expected to serve efficiently.

## Chaos-engineering attribute keys (unprefixed, not `agentmesh.*`)

`sdk/python/agentmesh/chaos.py`'s fault-injection feature emits two
additional attributes when a fault fires. These are **deliberately
unprefixed** (not `agentmesh.chaos_injected`): the Collector's decoder
silently drops any *unrecognized* `agentmesh.*`-prefixed key rather than
passing it through (a real bug caught in the MCP Gateway's audit emitter
before this document was updated to state the rule explicitly) — an
unprefixed key rides the ordinary "any other attribute key" passthrough
path instead of requiring a decoder change for every new field.

| Attribute key | Type | Required | Meaning |
|---|---|---|---|
| `chaos.injected` | string (`"true"`/absent) | no | Present and equal to `"true"` when this span's outcome was a deliberately injected fault (`ChaosPolicy.maybe_apply` fired) rather than a natural one. Decodes into `span.Span.ChaosInjected` (bool) and `schema/clickhouse/002_chaos_columns.sql`'s `chaos_injected` column. |
| `chaos.fault_type` | string | no, but present whenever `chaos.injected` is | One of `"latency"` or `"error"` (`chaos.LatencyFault`/`chaos.ErrorFault`'s `kind` field). Decodes into `span.Span.ChaosFaultType` and the `chaos_fault_type` column. |

## Payload size threshold (corrected — see note)

**Correction:** an earlier revision of this document stated the 4KB
inline/blob-ref decision was the exporter's responsibility. That
contradicted `System Design.md` §3's ingestion sequence diagram
("Coll->>Coll: size-check payload / alt payload >= 4KB / Coll->>Coll:
write blob to S3-compatible store") and `Architecture.md` §2's Collector
responsibility list, both of which are the authoritative source here —
caught while implementing `services/collector/internal/blobstore` and
fixed before any exporter shipped the wrong behavior in a released SDK
version.

The correct division of responsibility: **every exporter always sends the
full payload inline** (`agentmesh.input.inline` / `agentmesh.output.inline`),
regardless of size — an exporter never uploads to blob storage directly and
never sets `*.blob_ref` itself. The **Collector** performs the 4KB
size-check on ingestion; a payload at or above the threshold is uploaded to
the blob store (`services/collector/internal/blobstore`, key layout per
`System Design.md` §2.3) and the span persisted to ClickHouse with
`input_blob_ref`/`output_blob_ref` set and `input_inline`/`output_inline`
left NULL, rather than storing the same payload in both places.

This is deliberate, not incidental: giving every customer's agent process
write credentials to AgentMesh's object storage (the alternative, if the
SDK did the upload) is a real security anti-pattern this design avoids —
the SDK's only network dependency is the Collector's OTLP endpoint, exactly
as `Architecture.md` §2 describes it ("Runs inside the customer's process —
never sees the Trace Store directly").

A caller-set `*.blob_ref` attribute on an inbound span is therefore
rejected by the Collector as a protocol violation (`CodeInvalidArgument`)
rather than trusted — an exporter has no legitimate reason to set it.

## Authentication

The API key travels as gRPC metadata, not as an OTLP attribute: header key
`x-agentmesh-api-key`, value the raw key string (e.g. `am_live_...`). This
keeps authentication out of the span data model entirely — an attribute-based
API key would leak into every trace's stored data, which is exactly the kind
of accidental sensitive-data capture `Risks.md`'s Security Risks section
warns against.
