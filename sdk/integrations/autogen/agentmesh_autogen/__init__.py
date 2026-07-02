"""agentmesh_autogen: AgentMesh reference integration for AutoGen-style
conversational multi-agent frameworks (Milestone 3).

See `agentmesh_autogen.adapter` for the `AutoGenAdapter` implementation and
its module docstring for the framework-to-AgentMesh span mapping this
adapter implements, and `LIMITATIONS.md` (in this package's directory)
for what it intentionally does not cover.
"""

from .adapter import AutoGenAdapter

__all__ = ["AutoGenAdapter"]
