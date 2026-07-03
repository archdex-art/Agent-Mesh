import json
import os
from unittest import mock
import pytest

import agentmesh
from agentmesh import SpanStatus
from agentmesh.replay_shim import ReplayLookupError, ReplayedCallError, reset_call_counters


@pytest.fixture
def mock_replay_engine():
    # Setup global tracer for testing, with dummy keys
    agentmesh.configure(project_id="test-project", api_key="am_live_dummy")

    with mock.patch("urllib.request.urlopen") as mock_urlopen:
        yield mock_urlopen

    # Teardown
    agentmesh.tracer._global_tracer = None


def test_replay_shim_intercepts_call(mock_replay_engine, monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "test-replay-id")
    reset_call_counters()

    # The mocked engine returns a dummy OK response
    mock_response = mock.MagicMock()
    mock_response.read.return_value = json.dumps({
        "status": "ok",
        "output": "replayed answer"
    }).encode("utf-8")
    mock_response.__enter__.return_value = mock_response
    mock_replay_engine.return_value = mock_response

    @agentmesh.trace_tool_call()
    def fake_tool():
        return "original answer"

    # Call the tool, it should NOT return "original answer"
    # because the shim intercepts it
    result = fake_tool()
    assert result == "replayed answer"

    # Verify that the correct API was called
    mock_replay_engine.assert_called_once()
    req = mock_replay_engine.call_args[0][0]
    assert req.get_full_url().startswith("http://localhost:8090/v1/replay/test-replay-id/lookup")


def test_replay_shim_handles_error(mock_replay_engine, monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "test-replay-id")
    reset_call_counters()
    mock_response = mock.MagicMock()
    mock_response.read.return_value = json.dumps({
        "status": "error",
        "output": "something went wrong"
    }).encode("utf-8")
    mock_response.__enter__.return_value = mock_response
    mock_replay_engine.return_value = mock_response

    @agentmesh.trace_llm_call(name="fake_llm")
    def fake_llm():
        return "original llm answer"

    with pytest.raises(ReplayedCallError) as exc_info:
        fake_llm()
    
    assert exc_info.value.status == SpanStatus.ERROR
    assert exc_info.value.output == "something went wrong"


def test_replay_shim_inactive_without_env_var(mock_replay_engine, monkeypatch):
    monkeypatch.delenv("AGENTMESH_REPLAY_ID", raising=False)
    reset_call_counters()

    @agentmesh.trace_tool_call()
    def fake_tool():
        return "original answer"

    result = fake_tool()
    
    # Should execute the real function
    assert result == "original answer"
    mock_replay_engine.assert_not_called()

def test_replay_shim_with_custom_agent(mock_replay_engine, monkeypatch):
    monkeypatch.setenv("AGENTMESH_REPLAY_ID", "test-agent-replay-id")
    reset_call_counters()

    @agentmesh.trace_tool_call()
    def search_tool(query):
        return "real search result"
        
    @agentmesh.trace_llm_call(name="llm_summarize")
    def llm_summarize(text):
        return "real summary"

    def simple_agent(topic):
        result = search_tool(topic)
        summary = llm_summarize(result)
        return summary

    call_responses = [
        {"status": "ok", "output": "replayed search result"},
        {"status": "ok", "output": "replayed summary"},
    ]
    
    def mock_read():
        response = call_responses.pop(0)
        return json.dumps(response).encode("utf-8")
        
    mock_response = mock.MagicMock()
    mock_response.read.side_effect = mock_read
    mock_response.__enter__.return_value = mock_response
    mock_replay_engine.return_value = mock_response

    final_output = simple_agent("agent observability")
    
    assert final_output == "replayed summary"
    assert mock_replay_engine.call_count == 2
    
    # Verify correct calls were made
    call1_url = mock_replay_engine.call_args_list[0][0][0].get_full_url()
    call2_url = mock_replay_engine.call_args_list[1][0][0].get_full_url()
    
    assert "kind=tool.call" in call1_url
    assert "name=search_tool" in call1_url
    assert "call_index=0" in call1_url
    
    assert "kind=llm.call" in call2_url
    assert "name=llm_summarize" in call2_url
    assert "call_index=0" in call2_url
