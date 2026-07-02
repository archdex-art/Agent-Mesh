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
