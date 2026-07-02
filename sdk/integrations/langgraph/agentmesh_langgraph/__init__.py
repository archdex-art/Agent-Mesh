"""agentmesh_langgraph: AgentMesh reference integration for LangGraph
(Milestone 3).

See `agentmesh_langgraph.adapter` for the full design rationale and
`LIMITATIONS.md` for documented fidelity gaps.
"""

from .adapter import LangGraphAdapter, NODE_KIND_METADATA_KEY, node_kind_metadata

__all__ = ["LangGraphAdapter", "NODE_KIND_METADATA_KEY", "node_kind_metadata"]
