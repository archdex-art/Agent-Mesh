from agentmesh._span import Span, SpanKind, SpanStatus


def test_new_span_has_correct_id_lengths():
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    assert len(span.trace_id) == 32
    assert len(span.span_id) == 16
    # must be valid hex
    int(span.trace_id, 16)
    int(span.span_id, 16)


def test_new_spans_have_unique_ids():
    a = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="a")
    b = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="b")
    assert a.trace_id != b.trace_id
    assert a.span_id != b.span_id


def test_finish_sets_status_and_output():
    span = Span(project_id="p1", kind=SpanKind.TOOL_CALL, name="search")
    span.finish(status=SpanStatus.OK, output='{"result":"ok"}')
    assert span.status == SpanStatus.OK
    assert span.output == '{"result":"ok"}'
    assert span.end_time_ns is not None
    assert span.end_time_ns >= span.start_time_ns


def test_finish_records_tokens_and_cost():
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    span.finish(status=SpanStatus.OK, token_input=10, token_output=5, cost_usd=0.0021)
    assert span.token_input == 10
    assert span.token_output == 5
    assert span.cost_usd == 0.0021


def test_cost_usd_omitted_stays_none_never_zero():
    # System Design.md §7: absent cost must stay None (unknown), not become 0.0.
    span = Span(project_id="p1", kind=SpanKind.TOOL_CALL, name="search")
    span.finish(status=SpanStatus.OK)
    assert span.cost_usd is None


def test_set_input_stores_value_verbatim():
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    span.set_input('{"prompt":"hi"}')
    assert span.input == '{"prompt":"hi"}'


def test_set_input_sends_oversized_payload_fully_inline_not_truncated():
    # docs/otlp-mapping.md's corrected "Payload size threshold" section:
    # the SDK never truncates or offloads to blob storage — the Collector
    # makes the 4KB inline/blob-ref decision on ingestion. A payload well
    # over 4KB must still arrive at the exporter completely intact.
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    huge = "x" * 10_000
    span.set_input(huge)
    assert span.input == huge
    assert len(span.input) == 10_000


def test_finish_sends_oversized_output_fully_inline_not_truncated():
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    huge = "y" * 10_000
    span.finish(status=SpanStatus.OK, output=huge)
    assert span.output == huge
    assert len(span.output) == 10_000


def test_parent_span_id_defaults_to_none():
    span = Span(project_id="p1", kind=SpanKind.LLM_CALL, name="gpt-4.1")
    assert span.parent_span_id is None
