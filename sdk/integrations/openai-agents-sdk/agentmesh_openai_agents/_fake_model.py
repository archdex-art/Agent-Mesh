"""A deterministic, no-network `agents.models.Model`/`ModelProvider` test
double for exercising `AgentMeshTracingProcessor` and the example app
without ever calling a real LLM.

Implements the real `agents.models.interface.Model.get_response()`
contract (`openai-agents` 0.17.x): given the current conversation
``input`` (a string on the first call, a list of Responses-API input
items on every call after), it returns a scripted `ModelResponse` built
from `openai.types.responses` item types (`ResponseOutputMessage` for a
plain text answer, `ResponseFunctionToolCall` for a tool/handoff call) —
the same item types `agents.models.openai_chatcompletions` produces for a
real OpenAI response. Like that real model implementation, every call
opens its own `agents.tracing.generation_span(...)`, so
`AgentMeshTracingProcessor` sees one `llm.call` span per simulated model
turn exactly as it would for a real model.

``ScriptedTurn``s are consumed strictly in order (one per `get_response`
call) rather than matched by searching the whole growing conversation
history for `prompt_match` — history is cumulative across turns, so a
substring that matched an earlier turn would spuriously keep matching
forever. Order-based consumption is also how these scripts are actually
authored: as a fixed sequence of expected model calls for one scenario.
``prompt_match`` is kept as a lightweight self-check: when non-empty, it
must appear in that call's `input`, or `FakeModel` raises `AssertionError`
immediately — catching a scenario whose scripted turns silently drifted
out of sync with the agents/tools that actually ran.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from typing import Any

from agents.models.interface import Model, ModelProvider, ModelTracing
from agents.tracing import generation_span
from agents.usage import Usage
from openai.types.responses import (
    ResponseFunctionToolCall,
    ResponseOutputMessage,
    ResponseOutputText,
)

try:  # pragma: no cover - import path is stable across 0.17.x, guarded defensively
    from agents.items import ModelResponse
except ImportError:  # pragma: no cover
    from agents.models.interface import ModelResponse


@dataclass
class ScriptedTurn:
    """One canned model turn.

    ``tool_calls``, when given, is a list of
    ``{"id": str, "function": {"name": str, "arguments": json_str}}``
    dicts (the same shape as an OpenAI chat-completions tool call) — one
    `ResponseFunctionToolCall` output item is produced per entry. Otherwise
    ``response`` is returned as a single plain-text assistant message.
    """

    prompt_match: str
    response: str = ""
    tool_calls: list[dict] | None = None


class FakeModel(Model):
    """Scripted `Model`: consumes one `ScriptedTurn` per `get_response()`
    call, in order. See module docstring for the matching/ordering
    contract.
    """

    def __init__(self, turns: list[ScriptedTurn], *, name: str = "fake-model") -> None:
        self.name = name
        self.turns = list(turns)
        self._index = 0

    def _next_turn(self, input_data: Any) -> ScriptedTurn:
        if self._index >= len(self.turns):
            raise AssertionError(
                f"FakeModel ran out of scripted turns after {self._index}; "
                f"next model input was: {input_data!r}"
            )
        turn = self.turns[self._index]
        self._index += 1
        if turn.prompt_match:
            haystack = str(input_data)
            if turn.prompt_match not in haystack:
                raise AssertionError(
                    f"ScriptedTurn #{self._index} expected prompt_match "
                    f"{turn.prompt_match!r} in model input, got: {haystack!r}"
                )
        return turn

    async def get_response(
        self,
        system_instructions: str | None,
        input: str | list[dict],
        model_settings: Any,
        tools: list[Any],
        output_schema: Any,
        handoffs: list[Any],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None = None,
        conversation_id: str | None = None,
        prompt: Any = None,
    ) -> "ModelResponse":
        turn = self._next_turn(input)

        with generation_span(model=self.name, disabled=tracing == ModelTracing.DISABLED) as span:
            span.span_data.input = input if isinstance(input, list) else [{"role": "user", "content": input}]

            if turn.tool_calls:
                output_items: list[Any] = [
                    ResponseFunctionToolCall(
                        id=f"fc_{uuid.uuid4().hex[:8]}",
                        call_id=call.get("id", f"call_{uuid.uuid4().hex[:8]}"),
                        name=call["function"]["name"],
                        arguments=call["function"]["arguments"],
                        type="function_call",
                    )
                    for call in turn.tool_calls
                ]
            else:
                output_items = [
                    ResponseOutputMessage(
                        id=f"msg_{uuid.uuid4().hex[:8]}",
                        role="assistant",
                        status="completed",
                        type="message",
                        content=[ResponseOutputText(text=turn.response, type="output_text", annotations=[])],
                    )
                ]

            usage = Usage(requests=1, input_tokens=10, output_tokens=20, total_tokens=30)
            span.span_data.output = [item.model_dump() for item in output_items]
            span.span_data.usage = {"input_tokens": usage.input_tokens, "output_tokens": usage.output_tokens}

        return ModelResponse(output=output_items, usage=usage, response_id=None)

    async def stream_response(self, *args: Any, **kwargs: Any):
        raise NotImplementedError("FakeModel is scripted/turn-based only; it does not support streaming.")


class FakeModelProvider(ModelProvider):
    """`ModelProvider` handing out a single shared `FakeModel` so its
    scripted-turn index stays in sync across every agent in one `Runner`
    run (a handoff switches the *agent*, not the underlying fake model).
    """

    def __init__(self, turns: list[ScriptedTurn]) -> None:
        self._model = FakeModel(turns)

    def get_model(self, model_name: str | None = None) -> Model:
        return self._model
