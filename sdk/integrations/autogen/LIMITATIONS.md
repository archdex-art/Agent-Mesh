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
