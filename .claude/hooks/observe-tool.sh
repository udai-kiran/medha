#!/usr/bin/env bash
# PostToolUse hook — sends tool usage to agent-mem as an observation.
# Fires async (disowned background process) so it never blocks Claude Code.

command -v jq >/dev/null 2>&1 || exit 0
command -v curl >/dev/null 2>&1 || exit 0

INPUT=$(cat)
# Exit cleanly if input is not valid JSON (e.g. unexpected hook format).
printf '%s' "$INPUT" | jq empty 2>/dev/null || exit 0
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
[ -z "$SESSION_ID" ] && exit 0

CWD="$(pwd)"

_agentmem_env_file() {
    if [ -n "${AGENTMEMORY_ENV_FILE:-}" ] && [ -f "$AGENTMEMORY_ENV_FILE" ]; then
        printf '%s' "$AGENTMEMORY_ENV_FILE"; return
    fi
    local d="$CWD"
    while [ "$d" != "/" ]; do
        [ -f "$d/.env.mcp" ] && { printf '%s' "$d/.env.mcp"; return; }
        d=$(dirname "$d")
    done
    local cfg="${XDG_CONFIG_HOME:-$HOME/.config}/agent-mem/.env.mcp"
    [ -f "$cfg" ] && printf '%s' "$cfg"
}

if [ -z "${AGENTMEMORY_URL:-}" ] || [ -z "${AGENTMEMORY_SECRET:-}" ]; then
    _ENV=$(_agentmem_env_file)
    if [ -n "$_ENV" ]; then
        [ -z "${AGENTMEMORY_URL:-}" ]    && AGENTMEMORY_URL=$(grep    '^AGENTMEMORY_URL='    "$_ENV" 2>/dev/null | cut -d= -f2-)
        [ -z "${AGENTMEMORY_SECRET:-}" ] && AGENTMEMORY_SECRET=$(grep '^AGENTMEMORY_SECRET=' "$_ENV" 2>/dev/null | cut -d= -f2-)
    fi
fi
AGENTMEMORY_URL="${AGENTMEMORY_URL:-http://localhost:3111}"

PROJECT=$(git -C "$CWD" rev-parse --show-toplevel 2>/dev/null | xargs basename 2>/dev/null || basename "$CWD")
_BRANCH=$(git -C "$CWD" rev-parse --abbrev-ref HEAD 2>/dev/null)
PROJECT="${PROJECT}${_BRANCH:+/$_BRANCH}"
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)

PAYLOAD=$(printf '%s' "$INPUT" | jq -c \
  --arg ts "$TS" \
  --arg cwd "$CWD" \
  --arg project "$PROJECT" \
  '{
    hookType: "post_tool_use",
    sessionId: .session_id,
    project: $project,
    cwd: $cwd,
    timestamp: $ts,
    data: {
      tool_name: (.tool_name // ""),
      tool_input: (.tool_input // {}),
      tool_output: (.tool_response // "")
    }
  }')

CURL_ARGS=(-s -o /dev/null --max-time 5
  -X POST "$AGENTMEMORY_URL/agentmemory/observe"
  -H "Content-Type: application/json")
[ -n "${AGENTMEMORY_SECRET:-}" ] && CURL_ARGS+=(-H "Authorization: Bearer $AGENTMEMORY_SECRET")
CURL_ARGS+=(-d "$PAYLOAD")

curl "${CURL_ARGS[@]}" &
disown
exit 0
