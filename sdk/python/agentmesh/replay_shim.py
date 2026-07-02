"""AgentMesh SDK's replay shim: intercepts LLM/tool calls during the
Replay Engine's execution-mode replay and returns recorded historical
responses instead of invoking the real call, per Architecture.md §7
("intercept each tool call and return the recorded response instead of
calling the real tool") and System Design.md §4's replay flow
("AgentCode->>Replay: tool call intercepted via SDK's replay shim").

This is a mode flag in the SDK itself, not the Replay Engine reaching into
the agent process from outside (System Design.md §4: "rather than the
Replay Engine trying to monkey-patch the agent's tool dispatcher from the
outside"): when AGENTMESH_REPLAY_ID is set, tracer.py's decorator wrapper
— shared by both @trace_llm_call and @trace_tool_call, see that module's
docstring — calls into this module instead of executing the wrapped
function.

Applying this to LLM calls as well as tool calls, not just tool calls
narrowly, is a deliberate reading of the spec: System Design.md's stated
execution-mode goal is running "current code + historical data" to
completion. A real (non-replayed) LLM call during execution-mode replay
would immediately diverge into non-reproducible model output, defeating
the entire purpose of a deterministic re-run — so every decorated call,
LLM or tool, is replayed uniformly.
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from collections import defaultdict
from dataclasses import dataclass

from ._span import SpanStatus

_REPLAY_ID_ENV_VAR = "AGENTMESH_REPLAY_ID"
_REPLAY_ENGINE_ADDR_ENV_VAR = "AGENTMESH_REPLAY_ENGINE_ADDR"
_DEFAULT_REPLAY_ENGINE_ADDR = "http://localhost:8090"
_LOOKUP_TIMEOUT_SECONDS = 10.0


class ReplayLookupError(Exception):
    """Raised when the Replay Engine has no recorded response for the
    requested call position, or is unreachable. This never falls back to
    executing the real call — a replay that silently degrades into live
    execution would violate the determinism guarantee the whole feature
    exists to provide (Architecture.md §7)."""


class ReplayedCallError(Exception):
    """Raised in place of whatever exception the original (non-OK) call
    produced, so the agent's own error-handling branches — retry logic,
    fallback paths — execute the same way they did in the original trace.

    A known, documented limitation: the original exception's concrete
    type is not recoverable from a recorded span (only its status and
    string output are), so callers that pattern-match on a specific
    exception class rather than a broad `except Exception` will not
    replay identically. This mirrors _json_or_str's stated best-effort
    philosophy elsewhere in this SDK."""

    def __init__(self, status: SpanStatus, output: object) -> None:
        super().__init__(f"replayed call recorded status={status.value}: {output!r}")
        self.status = status
        self.output = output


@dataclass(frozen=True)
class RecordedCall:
    """A single recorded span's replayable result, as returned by the
    Replay Engine's lookup endpoint."""

    output: str | None
    status: SpanStatus


# Per-(kind, name) call counters, reset per process. A replay execution is
# a single process run reconstructing one trace, so a call's position
# within that run is exactly "the Nth time this (kind, name) pair was
# invoked" — matching how the Replay Engine indexes recorded spans for the
# same trace_id (System Design.md §4).
_call_counters: dict[tuple[str, str], int] = defaultdict(int)


def reset_call_counters() -> None:
    """Resets per-(kind, name) call-position counters. Exposed for tests
    and for a long-lived process replaying multiple traces sequentially
    (each replay run must start counting from zero again)."""
    _call_counters.clear()


def is_active() -> bool:
    """True when AGENTMESH_REPLAY_ID is set, meaning the current process
    is running under execution-mode replay and every decorated call must
    be intercepted rather than executed."""
    return bool(os.environ.get(_REPLAY_ID_ENV_VAR))


def current_replay_id() -> str | None:
    """Returns the active AGENTMESH_REPLAY_ID, or None if replay mode is
    not active. Unlike _replay_id(), never raises — used by tracer.py to
    tag every span with its source replay run without needing its own
    is_active() check first."""
    return os.environ.get(_REPLAY_ID_ENV_VAR) or None


def _replay_id() -> str:
    replay_id = os.environ.get(_REPLAY_ID_ENV_VAR)
    if not replay_id:
        raise ReplayLookupError(f"{_REPLAY_ID_ENV_VAR} is not set")
    return replay_id


def _engine_addr() -> str:
    return os.environ.get(_REPLAY_ENGINE_ADDR_ENV_VAR, _DEFAULT_REPLAY_ENGINE_ADDR)


def next_call_index(kind: str, name: str) -> int:
    """Returns the 0-based position of this call among all calls to the
    same (kind, name) pair so far in this process, then increments the
    counter for next time."""
    index = _call_counters[(kind, name)]
    _call_counters[(kind, name)] += 1
    return index


def fetch_recorded_response(kind: str, name: str, call_index: int) -> RecordedCall:
    """Looks up the recorded response for the call_index'th span of the
    given (kind, name) within the active replay's source trace, via the
    Replay Engine's HTTP lookup API.

    This endpoint is an AgentMesh-internal contract between this SDK and
    services/replay-engine/internal/execution — not a customer-facing
    wire format, so it is not documented in docs/otlp-mapping.md (which
    covers only the OTLP export path to the Collector).
    """
    replay_id = _replay_id()
    query = urllib.parse.urlencode({"kind": kind, "name": name, "call_index": call_index})
    url = f"{_engine_addr()}/v1/replay/{urllib.parse.quote(replay_id)}/lookup?{query}"

    request = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=_LOOKUP_TIMEOUT_SECONDS) as response:
            payload = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise ReplayLookupError(
            f"replay engine returned {exc.code} for {kind} {name!r} call #{call_index}: {body}"
        ) from exc
    except urllib.error.URLError as exc:
        raise ReplayLookupError(f"replay engine unreachable at {_engine_addr()}: {exc.reason}") from exc

    status_str = payload.get("status", "ok")
    try:
        status = SpanStatus(status_str)
    except ValueError:
        raise ReplayLookupError(f"replay engine returned unknown status {status_str!r}") from None

    return RecordedCall(output=payload.get("output"), status=status)
