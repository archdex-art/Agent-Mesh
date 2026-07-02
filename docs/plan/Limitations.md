# `agentmesh-langgraph` — known limitations

Documented per the Milestone 3 plan's time-boxing rule: don't silently
drop fidelity, write down exactly what's unsupported and why. Verified
against `langgraph==1.2.7` / `langchain-core==1.4.8` (the versions
installed in this sandbox); behavior on other versions may differ since
none of this is public, versioned API — see the "why not a public API"
note at the bottom.

## 1. Node kind is developer-declared, not auto-inferred

Unlike CrewAI (`Agent`/`Task`) or the OpenAI Agents SDK (typed `Span`
subclasses per event), a LangGraph node is an arbitrary Python callable —
there is no native, introspectable "this node calls an LLM" vs. "this
node calls a tool" distinction at the graph level. `LangGraphAdapter`
requires the graph author to tag each node once at build time via
`add_node(..., metadata=node_kind_metadata(SpanKind.TOOL_CALL))` (see
`adapter.py`'s module docstring). An untagged node is treated as
structural (`kind=None`) — the correct default for LangGraph's own
routing/conditional-edge nodes, but it also means a workflow node the
author forgot to tag silently becomes structural bookkeeping rather than
an exported span, with no warning. This is an accepted tradeoff, not a
bug: forcing a guess (e.g. "assume every node is a tool call") would be
actively worse — a wrong, confident kind is harder to notice than a
missing span.

## 2. Parallel/fan-out supersteps are linearized

LangGraph's Pregel executor nests every node in the *same superstep*
directly under the single graph-invocation run (their native
`parent_run_id` is the graph root, not each other — verified empirically:
four sequential nodes in a linear graph all reported the same
`parent_run_id`). To produce the assignment's required chain shape
("parent_external_id = whichever node most recently transitioned into
this one"), the adapter tracks its own "chain tip" per graph invocation
and threads each new top-level node off of it. For a strictly linear
graph (the shared workflow) this exactly reconstructs the true execution
order. For a graph with genuine parallel branches (e.g. two nodes in one
superstep, or the `Send` API's map-style fan-out), this same mechanism
serializes them into an arbitrary chain based on callback delivery order,
not their true (parallel, no order) relationship. Time-boxed: correctly
representing fan-out/fan-in as a DAG rather than a chain would need a
distinct AgentMesh trace-shape concept the current 3-kind model doesn't
have a slot for, and the shared workflow this integration targets has no
branching to begin with.

## 3. A node with `add_conditional_edges` attached fires twice

Empirically, a node that is also the *source* of `add_conditional_edges`
gets wrapped by LangGraph in an internal branch-executor sequence: its
top-level `on_chain_start`/`on_chain_end` pair fires as usual, plus a
second, nested pair (parented to the first) that inherits the *same*
`metadata`, including any `agentmesh_kind` tag. The adapter treats any
`on_chain_start` that isn't a direct child of a tracked graph root as
structural regardless of its inherited metadata, specifically to avoid
exporting that node's kind twice. Practical guidance: keep routing logic
on a dedicated, untagged node rather than attaching
`add_conditional_edges` directly to a tagged LLM/tool/handoff node.

## 4. `SpanTracker` has no public `Span.input` setter

Per `agentmesh_integrations_common.context.SpanTracker`'s own documented
gap (not LangGraph-specific): there is no supported way to set
`span.input` before `finish()`. A LangGraph node's incoming state would
be the natural candidate for `.input`, but this adapter only records
`output` (the node's return value / model response / tool output) at
`finish()` time, plus whatever start-time context fits in
`attributes` (`framework`, `langgraph.node`/`langgraph.model`/
`langgraph.tool`). `span.input` is left unset for every span this
adapter exports.

## 5. Only `invoke`/`ainvoke`/`stream`/`astream` are wrapped

`instrument()` patches those four methods on the compiled graph (or,
for an uncompiled `StateGraph`, wraps `.compile()` so the graph it
produces is patched the same way). `.astream_events()` and any other
LangGraph entry point that does not thread a `RunnableConfig` through the
same way are not wrapped — calling them on an instrumented graph runs
un-instrumented. Not exercised by the shared workflow or this package's
test suite.

## 6. Retries re-run as fresh, unlinked spans

LangGraph's per-node `retry_policy` re-invokes a node's callable on
transient failure; each attempt gets its own fresh `run_id` (its own
`on_chain_start`/`on_chain_end` pair) rather than reusing the original
attempt's id. The adapter has no way to distinguish "attempt 2 of node
X" from "a second, unrelated call to node X" from the callback stream
alone, so retried nodes show up as N sequential sibling spans in the
chain rather than as one span with N attempts. Not exercised by the
shared workflow (its nodes are deterministic and never retry).

## Why this isn't built against a narrower/pinned "public API"

LangGraph does not publish a stable "tracing hook" API of its own for
third-party observability integrations (as of `1.2.7`); the closest
first-class extension points are `langchain-core`'s general-purpose
`BaseCallbackHandler` (which every `Runnable`, including a compiled
LangGraph graph, accepts via `RunnableConfig`) and `add_node`'s
`metadata` passthrough (also general-purpose, shared with LangSmith
tracing). Both are documented, versioned parts of `langchain-core`'s
public surface, not private/internal APIs — but the specific *shape* of
the metadata LangGraph attaches per callback event (`langgraph_node`,
`langgraph_step`, `langgraph_triggers`, ...) is Pregel-internal
convention rather than a documented contract, so a LangGraph major
version bump could change key names or nesting behavior without notice.
This is the same category of risk every framework adapter in this repo
accepts (e.g. `agentmesh_openai_agents.processor` matching on
`agents.tracing`'s concrete `Span` subclasses).
# CrewAI adapter limitations

Documented per the Milestone 3 test-strategy time-boxing rule: fidelity
gaps below are known and accepted, not silently dropped.

## No `Span.input` population

`agentmesh_integrations_common.context.SpanTracker.start()` does not
expose a way to set `Span.input` before `finish()` is called (it only
accepts `attributes`, threaded onto the exported span's `attributes`
dict). This adapter -- like every other Milestone 3 adapter built on
`agentmesh_integrations_common` -- never populates `Span.input`. Where
useful, start-time-only context (e.g. a task's description) is instead
recorded as a string attribute (see `CrewAIAdapter.instrument`'s
`crewai.task_description` attribute).

## `step_callback` is not invoked for a task's terminal "Final Answer" step

Confirmed against the installed `crewai` version's default agent
executor (`crewai.experimental.agent_executor.AgentExecutor`, which
`Agent.executor_class` defaults to): `step_callback` fires for every
intermediate ReAct tool-decision step (an `AgentAction`, twice per
step -- once via `crewai.utilities.agent_utils`'s tool-result handling,
once via the executor's own `_invoke_step_callback` call after the tool
runs), but it is **not** invoked at all for the step that produces the
task's final `AgentFinish` answer when that answer is reached through
the text/ReAct parsing path (`call_llm_and_parse` ->
`route_by_answer_type` -> `_route_finish_with_todos("agent_finished")`
never calls `_invoke_step_callback`). The "native tool calling" code
path (`call_llm_native_tools`) does call `_invoke_step_callback` for its
own final-answer case, but text-based fake/scripted LLMs (the only kind
used in this integration's tests and example, per Milestone 3's
no-network-calls rule) always take the ReAct/text path.

Practical effect: a task whose agent immediately returns a final answer
without ever using a tool (no `AgentAction` step) produces **zero**
`llm.call` spans for that task -- only its `agent.handoff` span (from
`task_callback`). This is a CrewAI behavior, not an
`agentmesh_crewai`/`SpanTracker` bug: no `agentmesh` API can observe an
event CrewAI itself never calls back for. `tests/test_shared_workflow.py`
accounts for this directly (its reviewer agent makes one tool call
before finishing, specifically so its `llm.call` span count is
non-zero and testable) and documents the exact call counts involved.

## Task-to-step parent resolution assumes `Process.sequential`

`CrewAIAdapter` resolves "which task is this step part of" via a single
cursor over `crew.tasks`, advanced by one every time `task_callback`
fires (see `agentmesh_crewai.adapter`'s module docstring for why: CrewAI
gives no native "task started" callback to hook a `SpanTracker.start()`
call to). This is correct for `Process.sequential` -- the process kind
this integration's tests and example use, and CrewAI's default -- where
exactly one task is ever "in flight" at a time, in `crew.tasks` order.

It is not guaranteed correct for:

- `Process.hierarchical`, where a manager agent can interleave work
  across delegate agents in an order that does not match `crew.tasks`'
  static list order.
- Tasks run with `async_execution=True`, where two tasks (and therefore
  two agents' `step_callback`s) can genuinely be in flight concurrently;
  the single shared cursor has no way to attribute a step to the correct
  one of two simultaneously-open tasks in that case.

Both are out of scope for this reference integration's tested/supported
mode (`Process.sequential`, synchronous tasks) per the Milestone 3
time-boxing rule; a step misattributed under either mode still links
somewhere in the same single trace (no orphan spans, no crash) rather
than being dropped, since the cursor always resolves to *some* tracked
task (or the crew-level structural root as a fallback).

## Task-level `agent.handoff` spans do not have an accurate start time

Every task's `agent.handoff` span is opened (via `SpanTracker.start()`)
inside `CrewAIAdapter.instrument()`, before `crew.kickoff()` ever runs --
not when that specific task actually starts executing -- because CrewAI
exposes no "task started" hook the adapter could use to open it later.
This is what lets every step that happens *during* a task's run resolve
that task's span as its parent (see the module docstring for why the
original, pre-`SpanTracker` implementation could not do this at all).
The consequence is that a handoff span's `start_time_ns` reflects
instrumentation time, not the task's actual start time, so its recorded
*duration* is not meaningful for any task after the first. Span
structure (parent/child linkage, kind, count) is unaffected.
# AutoGen adapter: known limitations

Per the approved Milestone 3 plan, AutoGen's conversation-style execution
was expected to require the most normalization effort of the four
reference integrations. This document records what `AutoGenAdapter`
covers, what it deliberately does not, and why.

## Package used: `ag2`, not `pyautogen` or `autogen-agentchat`

"AutoGen" on PyPI has had several name/ownership transitions. In this
adapter's development/test sandbox (network access available), all three
candidates named in the assignment were tried:

- `pip install pyautogen` **succeeded**, but resolved to a **0.10.0
  compatibility-shim** release whose `__init__.py` exports nothing at all
  (no `ConversableAgent`, `GroupChat`, or anything else usable) — a dead
  placeholder left behind by the original package's rename.
- `pip install autogen-agentchat` **succeeded** and installs Microsoft's
  actively-maintained fork (`autogen-core`/`autogen-agentchat`/
  `autogen-ext`, currently 0.7.5). This is a **fully rewritten, async-first
  API** (`AssistantAgent`, `RoundRobinGroupChat`, `SelectorGroupChat`, ...)
  with no `ConversableAgent`, `GroupChat`, `GroupChatManager`,
  `register_reply`, `execute_function`, or `hook_lists` — none of the
  surface this milestone's plan and `Architecture.md` §3's mapping table
  describe for AutoGen.
- `pip install ag2` **succeeded** and installs 0.14.0, which provides
  `import autogen` with exactly the classic, stable surface the plan
  names: `ConversableAgent`, `GroupChat`, `GroupChatManager`,
  `register_reply`, `execute_function`, `hook_lists`, etc. (`ag2` is the
  continuation of the original AutoGen project after Microsoft's fork
  diverged into the package above.)

`AutoGenAdapter` is built, and its test suite runs, against **`ag2`**
(imported as `autogen`) — the only one of the three that both installs
successfully *and* matches the framework shape this milestone specifies.
`sdk/integrations/autogen/pyproject.toml` depends on `ag2>=0.14.0`
accordingly. The test suite additionally guards this with
`pytest.importorskip("autogen", ...)` so that in an environment where none
of AutoGen's package variants can be installed, `pytest` skips cleanly
instead of failing — but in the environment this adapter was built and
verified in, `ag2` installed and every test in
`tests/test_autogen_adapter.py` **passed against the real package**, not
a skip. A future maintainer running this on a network-restricted host
should re-check `pip install ag2` succeeds; if it doesn't, the skip will
fire and this adapter's logic will be unverified until it does.

## Why three separate hooks instead of one "message-passing" hook

AutoGen has no single callback that already distinguishes "an LLM
generated this message" from "this message is a function/tool call" from
"this message got routed to a different agent" — `hook_lists`'
hookable methods (`process_last_received_message`,
`process_all_messages_before_reply`, `process_message_before_send`,
`update_agent_state`, and the `safeguard_*` hooks) all fire once per
*already-classified* concept AutoGen itself doesn't distinguish for us
(they see the message dict, not why the reply pipeline produced it), and
`register_hook`'s hookable methods do not include anything like an
`on_send`/`on_receive` pair with a return value we could act on before
the framework proceeds. So this adapter does **not** use `register_hook`/
`hook_lists` at all; instead it wraps three narrower, unambiguous points
directly (see `adapter.py`'s module docstring for the full mapping):

1. `ConversableAgent.execute_function` -> `tool.call` (exactly one span
   per function actually executed, atomic and unambiguous).
2. `ConversableAgent.generate_reply` -> `llm.call`, but **only** when the
   reply isn't a raw function/tool-result dict (`role in {"function",
   "tool"}`) that `execute_function` already captured — otherwise the same
   tool invocation would be double-counted as both `tool.call` and
   `llm.call`. A message that merely *proposes* a `function_call`/
   `tool_calls` entry (an LLM's tool-use decision, before execution) is
   still counted as `llm.call`, since generating that proposal is itself
   "agent message generation" distinct from the later `execute_function`
   call that actually runs it.
3. `GroupChat.select_speaker` -> `agent.handoff`, opened before the newly
   selected agent's `generate_reply` runs and closed once it returns, so
   that agent's own spans nest underneath the handoff that routed to it.

## Known gaps / things intentionally not solved

- **`GroupChatManager` itself is never instrumented.** In stock
  `GroupChatManager.run_chat`, the manager only orchestrates — it never
  independently generates conversational content or executes a function
  as a participant, so there was nothing to hook. A user who subclasses
  `GroupChatManager` to add manager-level LLM calls (uncommon) would not
  have those calls captured by this adapter.
- **The `select_speaker` -> `generate_reply` pairing is assumed, not
  enforced.** The adapter assumes stock `run_chat`'s contract that exactly
  one `generate_reply` call on the newly-selected speaker immediately
  follows each `select_speaker` call. `_wrap_select_speaker` defensively
  closes (as an error) any handoff span left open by a *second*
  `select_speaker` call before a `generate_reply` closed the first one, so
  a violation of this assumption degrades to an extra `ERROR`-status
  handoff span rather than a silent leak or a crash — but it will not
  produce a *correct* trace for a heavily customized `run_chat` override.
- **No public `Span.input` setter on `SpanTracker`.** Per the shared
  `context.SpanTracker` contract, there is no way to set a span's `input`
  field before `finish()`. `tool.call` spans carry the function's raw JSON
  arguments in `span.attributes["autogen.function_arguments"]` instead of
  `span.input`; `llm.call`/`agent.handoff` spans don't carry their
  "input" (the preceding conversation messages) at all — only their
  `output` (the generated reply / the reply produced by the newly routed
  agent). A maintainer who later gets a public `SpanTracker` input-setter
  should backfill this rather than reach into `SpanTracker`'s internals.
- **The adapter's root structural span is never `finish()`-ed.** One
  `AutoGenAdapter` instance is designed to instrument exactly one logical
  conversation; its `__init__` opens a single persistent `kind=None` root
  so that every top-level event (a direct `generate_reply()` call, a
  later `initiate_chat()`, ...) shares one `trace_id` even though AutoGen
  has no single "conversation started"/"conversation ended" callback to
  bracket that root against. Since `kind=None` entries are never exported
  as `Span`s, this has no effect on exported trace content, but it does
  leave one harmless bookkeeping entry alive in
  `SpanTracker._tracked` for the adapter's lifetime — construct a fresh
  `AutoGenAdapter` per conversation (as the tests and
  `examples/autogen-debate/main.py` both do) rather than reusing one
  across unrelated conversations.
- **Real (non-scripted) LLM-proposed tool calls are mapped but not
  exercised by this adapter's own tests.** The test suite and
  `examples/autogen-debate/main.py` both drive `execute_function`
  directly from a scripted `register_reply` function (to stay
  network-free and deterministic, matching every other Milestone 3 shared
  workflow demo). The `_is_llm_generation()` classification logic in
  `adapter.py` also handles the case where a real `llm_config`-backed
  agent's `generate_reply` returns a dict containing a `function_call`/
  `tool_calls` proposal (still classified as `llm.call`, per the module
  docstring), but that specific code path has no direct test coverage
  here since exercising it would require either a real LLM or building an
  additional OpenAI-wire-format test double beyond this adapter's
  time-boxed scope — a future maintainer adding a `FakeModel`-style test
  double (as `sdk/integrations/openai-agents-sdk` does for the OpenAI
  Agents SDK) could close this gap.
- **Broadcast fan-out messages are not spans.** When `GroupChatManager`
  broadcasts a message to every non-speaking participant
  (`run_chat`'s `self.send(message, agent, request_reply=False, ...)`
  loop), those `receive()` calls are not wrapped or exported in any form
  — they carry no new content (the speaking agent's own `generate_reply`
  already captured it) and wrapping them would only add noise, not
  fidelity.

## Verification performed

`cd sdk/integrations/autogen && pip install -e . -e ../common -e ../../python && python -m pytest tests/ -v`
was run in the sandbox this adapter was built in; `ag2` installed
successfully and all tests in `tests/test_autogen_adapter.py` passed
against the real package (not a skip). `examples/autogen-debate/main.py`
was run standalone (`python examples/autogen-debate/main.py`) and printed
a single trace id with the expected five-span summary (search, read_page,
summarize, the researcher -> reviewer handoff, and the reviewer's own
review generation nested under it).
