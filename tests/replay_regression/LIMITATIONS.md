# Replay Engine Limitations

Execution-mode replay (`AGENTMESH_REPLAY_ID`) works by intercepting function execution at the `@agentmesh.trace_tool_call()` and `@agentmesh.trace_llm_call()` decorators (via `replay_shim.py`).

## Frameworks Defying Execution Replay

Frameworks that orchestrate LLM and tool calls internally and emit observability data via read-only callbacks (such as LangGraph, CrewAI, AutoGen) **defy execution replay** out of the box because:

1. **No LLM Interception**: These frameworks use their own LLM clients (e.g., LangChain's `ChatOpenAI` for LangGraph/CrewAI). The AgentMesh SDK does not monkey-patch these clients. Since they are not wrapped with `@agentmesh.trace_llm_call()`, the Replay Shim cannot intercept them. During replay, the framework will make live network calls to the LLM provider instead of using the recorded trace data.
2. **Callback-Only Adapters**: The `agentmesh_langgraph` and `agentmesh_crewai` adapters rely on read-only event hooks (`BaseCallbackHandler` for LangGraph, task callbacks for CrewAI). These hooks are fired *after* or *around* the actual execution and cannot inject a mock return value or short-circuit the execution.
3. **Tool Interception Requires Explicit Wrapping**: For a tool call to be intercepted, the developer must explicitly wrap their tool function with `@agentmesh.trace_tool_call()`. If a tool is only wrapped with the framework's native decorator (e.g., CrewAI's `@tool` or LangChain's `@tool`), the shim is not triggered, and the tool will execute with real side effects.

## Workaround

To achieve execution replay in these frameworks, the Replay Engine would need deep, framework-specific monkey-patching or integration with the frameworks' own mock/testing utilities (e.g., replacing LangChain's LLM class with a custom `ReplayLLM` that fetches from `AGENTMESH_REPLAY_ID`). The current shim design is limited to bespoke Python code utilizing the core AgentMesh SDK decorators.
