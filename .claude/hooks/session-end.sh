#!/usr/bin/env bash
# Stop hook — records session_end to agent-mem, triggering consolidation
# (summarisation + memory distillation pipeline).
# Runs synchronously so the session is closed before Claude Code exits.

command -v jq >/dev/null 2>&1 || exit 0
command -v curl >/dev/null 2>&1 || exit 0

INPUT=$(cat)
printf '%s' "$INPUT" | jq empty 2>/dev/null || exit 0
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
[ -z "$SESSION_ID" ] && exit 0

AGENTMEMORY_URL="${AGENTMEMORY_URL:-http://localhost:3111}"

if [ -z "${AGENTMEMORY_SECRET:-}" ]; then
    ENV_FILE="$(cd "$(dirname "$0")/.." && pwd)/../.env.mcp"
    [ -f "$ENV_FILE" ] && AGENTMEMORY_SECRET=$(grep '^AGENTMEMORY_SECRET=' "$ENV_FILE" 2>/dev/null | cut -d= -f2-)
fi

CWD="$(pwd)"
PROJECT=$(git -C "$CWD" rev-parse --show-toplevel 2>/dev/null | xargs basename 2>/dev/null || basename "$CWD")
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)

PAYLOAD=$(jq -cn \
  --arg ts "$TS" \
  --arg cwd "$CWD" \
  --arg project "$PROJECT" \
  --arg sessionId "$SESSION_ID" \
  '{
    hookType: "session_end",
    sessionId: $sessionId,
    project: $project,
    cwd: $cwd,
    timestamp: $ts,
    data: {}
  }')

CURL_ARGS=(-s -o /dev/null --max-time 10
  -X POST "$AGENTMEMORY_URL/agentmemory/observe"
  -H "Content-Type: application/json")
[ -n "${AGENTMEMORY_SECRET:-}" ] && CURL_ARGS+=(-H "Authorization: Bearer $AGENTMEMORY_SECRET")
CURL_ARGS+=(-d "$PAYLOAD")

curl "${CURL_ARGS[@]}"
exit 0
