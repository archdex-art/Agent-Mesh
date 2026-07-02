from agentmesh._span import Span, SpanKind, SpanStatus, CURRENT_SCHEMA_VERSION
from agentmesh.exporter import encode_span, build_export_request


def _finished_span(**overrides):
    span = Span(project_id="21a3d950-35e3-44df-a015-4ed20121a13f", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    span.set_input('{"prompt":"hi"}')
    span.finish(status=SpanStatus.OK, output='{"text":"hello"}', token_input=10, token_output=5, cost_usd=0.0021)
    for key, value in overrides.items():
        setattr(span, key, value)
    return span


def _attr_map(otlp_span):
    return {kv.key: kv.value for kv in otlp_span.attributes}


def test_encode_span_sets_trace_and_span_id_bytes():
    span = _finished_span()
    otlp_span = encode_span(span)
    assert otlp_span.trace_id == bytes.fromhex(span.trace_id)
    assert otlp_span.span_id == bytes.fromhex(span.span_id)
    assert len(otlp_span.trace_id) == 16
    assert len(otlp_span.span_id) == 8


def test_encode_span_sets_required_attributes():
    span = _finished_span()
    otlp_span = encode_span(span)
    attrs = _attr_map(otlp_span)

    assert attrs["agentmesh.schema_version"].int_value == CURRENT_SCHEMA_VERSION
    assert attrs["agentmesh.project_id"].string_value == span.project_id
    assert attrs["agentmesh.span_kind"].string_value == "llm.call"
    assert attrs["agentmesh.status"].string_value == "ok"


def test_encode_span_sets_optional_payload_and_cost_attributes():
    span = _finished_span()
    otlp_span = encode_span(span)
    attrs = _attr_map(otlp_span)

    assert attrs["agentmesh.input.inline"].string_value == '{"prompt":"hi"}'
    assert attrs["agentmesh.output.inline"].string_value == '{"text":"hello"}'
    assert attrs["agentmesh.token.input"].int_value == 10
    assert attrs["agentmesh.token.output"].int_value == 5
    assert attrs["agentmesh.cost_usd"].double_value == 0.0021


def test_encode_span_omits_cost_attribute_when_none():
    # docs/otlp-mapping.md: absent cost must never be sent as 0.0.
    span = Span(project_id="p1", kind=SpanKind.TOOL_CALL, name="search")
    span.finish(status=SpanStatus.OK)
    otlp_span = encode_span(span)
    attrs = _attr_map(otlp_span)
    assert "agentmesh.cost_usd" not in attrs


def test_encode_span_includes_passthrough_attributes():
    span = _finished_span()
    span.attributes["framework"] = "langgraph"
    otlp_span = encode_span(span)
    attrs = _attr_map(otlp_span)
    assert attrs["framework"].string_value == "langgraph"


def test_encode_span_sets_parent_span_id_when_present():
    span = _finished_span(parent_span_id="0123456789abcdef")
    otlp_span = encode_span(span)
    assert otlp_span.parent_span_id == bytes.fromhex("0123456789abcdef")


def test_encode_span_omits_parent_span_id_for_root_span():
    span = _finished_span()  # parent_span_id defaults to None
    otlp_span = encode_span(span)
    assert otlp_span.parent_span_id == b""


def test_build_export_request_wraps_multiple_spans():
    spans = [_finished_span(), _finished_span()]
    request = build_export_request(spans)
    assert len(request.resource_spans) == 1
    assert len(request.resource_spans[0].scope_spans) == 1
    assert len(request.resource_spans[0].scope_spans[0].spans) == 2
