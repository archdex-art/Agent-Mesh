# `examples/shared/`

Framework-agnostic building blocks shared by the four Milestone 3 reference
integration example apps (`examples/langgraph-support-bot/`,
`examples/crewai-research-crew/`, `examples/autogen-debate/`,
`examples/openai-agents-sdk-handoff-demo/`).

## Why this exists

Milestone 3's success criteria (`docs/plan/Milestones.md`) require that "a
human reviewer can look at any of the four [example] traces and identify
the same logical steps without knowing which framework produced it." That
is only possible if every example app runs the *same* workflow with the
*same* inputs and outputs — otherwise differences in traces would reflect
differences in the demo, not differences in how each framework's adapter
maps onto the AgentMesh span model. `docs/plan/Feature Roadmap.md`'s
"Framework reference integrations" entry frames this as proving the
"framework-agnostic" claim concretely; comparable example apps are what
make that provable rather than asserted.

This directory centralizes the parts of that workflow that have nothing to
do with any particular orchestration framework, so each example app's
`main.py` only has to handle framework-specific wiring (how *that*
framework calls a tool, invokes an LLM, or hands off to a sub-agent) and
delegates the actual workflow content here:

```
Research topic -> Search tool -> Read page tool -> LLM summary
    -> Reviewer agent -> Return answer
```

- **`tools.py`** — `search_tool(query)` and `read_page_tool(url_or_ref)`.
  Deterministic, side-effect-free, no network access: same input always
  produces the same output, and `read_page_tool` resolves whatever
  `search_tool` points it at. This determinism is what makes traces
  reproducible and, later, usable as the golden-trace fixture corpus
  (Milestone 7).
- **`prompts.py`** — `RESEARCH_TOPIC`, `summarizer_prompt(...)`,
  `reviewer_prompt(...)`, and canned `FAKE_SUMMARY`/`FAKE_REVIEW` outputs
  matching the `CannedLLM`/`FakeLLM` pattern already used in
  `examples/crewai-research-crew/main.py` and
  `sdk/integrations/crewai/tests/test_crewai.py`, so no example app ever
  needs a real API key to run.
- **`fixtures.py`** — `expected_step_names()` and `WORKFLOW_STEPS`, the
  canonical ordered list of logical steps (and their expected span kind)
  every framework's trace should produce. This is a *structural* reference
  (step count, kind, and order) — it intentionally does not specify
  wire-exact span names, since those are each framework adapter's call.

## How to import it

`examples/shared/` is a plain module directory, not an installed package —
consistent with the existing example apps (see
`examples/crewai-research-crew/main.py`), which are simple top-level
`main.py` scripts with no local package install. Each example app adds it
to `sys.path` at import time:

```python
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent / "shared"))

import fixtures
import prompts
import tools
```

Then use it like any other local module, e.g.:

```python
notes = tools.read_page_tool(tools.search_tool(prompts.RESEARCH_TOPIC))
summary_prompt = prompts.summarizer_prompt(notes)
# ... hand summary_prompt to the framework's (canned) LLM ...
review_prompt = prompts.reviewer_prompt(prompts.FAKE_SUMMARY)
# ... hand review_prompt to the framework's reviewer agent ...
assert fixtures.expected_step_names() == ["search", "read_page", "summarize", "review"]
```
