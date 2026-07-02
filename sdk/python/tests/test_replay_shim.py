import json
import urllib.error

import pytest

from agentmesh._span import SpanStatus
from agentmesh.replay_shim import (
    ReplayLookupError,
    fetch_recorded_response,
    is_active,
    next_call_index,
    reset_call_counters,
)


@pytest.fixture(autouse=True)
def _reset_state(monkeypatch):
    reset_call_counters()
    monkeypatch.delenv("AGENTMESH_REPLAY_ID", raising=False)
    monkeypatch.delenv("AGENTMESH_REPLAY_ENGINE_ADDR", raising=False)
    yield
    reset_call_counters()


def test_is_active_false_when_env_var_unset():
    assert is_active() is False


def test_is_active_true_when_env_var_set(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")
    assert is_active() is True


def test_next_call_index_increments_per_kind_and_name():
    assert next_call_index("tool.call", "search") == 0
    assert next_call_index("tool.call", "search") == 1
    assert next_call_index("tool.call", "search") == 2


def test_next_call_index_is_independent_per_name():
    assert next_call_index("tool.call", "search") == 0
    assert next_call_index("tool.call", "fetch") == 0
    assert next_call_index("tool.call", "search") == 1


def test_reset_call_counters_clears_all_positions():
    next_call_index("tool.call", "search")
    next_call_index("tool.call", "search")
    reset_call_counters()
    assert next_call_index("tool.call", "search") == 0


def test_fetch_recorded_response_requires_replay_id(monkeypatch):
    monkeypatch.delenv("AGENTMESH_REPLAY_ID", raising=False)
    with pytest.raises(ReplayLookupError, match="AGENTMESH_REPLAY_ID"):
        fetch_recorded_response("tool.call", "search", 0)


def test_fetch_recorded_response_parses_successful_lookup(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

        def read(self):
            return json.dumps({"output": "recorded search result", "status": "ok"}).encode()

    captured_url = {}

    def fake_urlopen(request, timeout):
        captured_url["url"] = request.full_url
        return FakeResponse()

    monkeypatch.setattr("urllib.request.urlopen", fake_urlopen)

    recorded = fetch_recorded_response("tool.call", "search", 2)

    assert recorded.output == "recorded search result"
    assert recorded.status == SpanStatus.OK
    assert "/v1/replay/replay-123/lookup" in captured_url["url"]
    assert "kind=tool.call" in captured_url["url"]
    assert "name=search" in captured_url["url"]
    assert "call_index=2" in captured_url["url"]


def test_fetch_recorded_response_parses_error_status(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

        def read(self):
            return json.dumps({"output": "boom: timeout", "status": "error"}).encode()

    monkeypatch.setattr("urllib.request.urlopen", lambda request, timeout: FakeResponse())

    recorded = fetch_recorded_response("tool.call", "search", 0)
    assert recorded.status == SpanStatus.ERROR
    assert recorded.output == "boom: timeout"


def test_fetch_recorded_response_raises_on_http_404(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")

    def raise_404(request, timeout):
        raise urllib.error.HTTPError(
            request.full_url, 404, "not found", hdrs=None, fp=__import__("io").BytesIO(b"no recorded span at this position")
        )

    monkeypatch.setattr("urllib.request.urlopen", raise_404)

    with pytest.raises(ReplayLookupError, match="404"):
        fetch_recorded_response("tool.call", "search", 99)


def test_fetch_recorded_response_raises_on_unreachable_engine(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")

    def raise_unreachable(request, timeout):
        raise urllib.error.URLError("connection refused")

    monkeypatch.setattr("urllib.request.urlopen", raise_unreachable)

    with pytest.raises(ReplayLookupError, match="unreachable"):
        fetch_recorded_response("tool.call", "search", 0)


def test_fetch_recorded_response_raises_on_unknown_status(monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "replay-123")

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

        def read(self):
            return json.dumps({"output": "x", "status": "not-a-real-status"}).encode()

    monkeypatch.setattr("urllib.request.urlopen", lambda request, timeout: FakeResponse())

    with pytest.raises(ReplayLookupError, match="unknown status"):
        fetch_recorded_response("tool.call", "search", 0)
