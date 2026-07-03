#!/usr/bin/env bash
# One-command AgentMesh startup: brings up the full stack, waits for it
# to actually be reachable, and opens the Console in your browser —
# the only thing you should ever need to type to get started.
#
# Usage: ./run.sh          (from the repo root)
#        ./run.sh stop     (tear down, keep data)
#        ./run.sh reset    (tear down and wipe all data)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/deploy"

if [[ "${1:-}" == "stop" ]]; then
  echo "Stopping AgentMesh (data preserved)..."
  docker compose down
  exit 0
fi

if [[ "${1:-}" == "reset" ]]; then
  echo "Stopping AgentMesh and wiping all data..."
  docker compose down -v
  exit 0
fi

if ! docker info >/dev/null 2>&1; then
  echo "Docker doesn't seem to be running. Start Docker Desktop and try again." >&2
  exit 1
fi

echo "Starting AgentMesh (this can take a minute on first run)..."
docker compose up -d --build

echo "Waiting for the Query API to come up..."
for _ in $(seq 1 60); do
  if curl -sf -o /dev/null "http://localhost:${AGENTMESH_QUERYAPI_PORT:-8080}/v1/setup" -X OPTIONS 2>/dev/null; then
    break
  fi
  sleep 1
done

CONSOLE_URL="http://localhost:${AGENTMESH_CONSOLE_PORT:-3001}"
echo ""
echo "AgentMesh is up: ${CONSOLE_URL}"
echo ""

if command -v open >/dev/null 2>&1; then
  open "${CONSOLE_URL}"
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "${CONSOLE_URL}"
else
  echo "Open ${CONSOLE_URL} in your browser to get started."
fi
