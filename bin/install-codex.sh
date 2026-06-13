#!/usr/bin/env bash
# Writes/updates .codex/hooks.json to wire agent-mem-codex-hooks into a Codex project.
#
# Usage: ./bin/install-codex.sh [TARGET_DIR]
#
# TARGET_DIR defaults to the current working directory.
# Idempotent — safe to re-run to update an existing install.
#
# After installing, trust the hooks in Codex:
#   codex /hooks   →  select hooks → trust
# Or set them as globally trusted in ~/.codex/config.toml.
#
# Build the binary first:  make build-codex-hooks   (or: make build)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TARGET="${1:-$(pwd)}"
TARGET="$(cd "$TARGET" && pwd)"

CODEX_DIR="$TARGET/.codex"
HOOKS_JSON="$CODEX_DIR/hooks.json"
HOOKS_BIN_DIR="$CODEX_DIR/hooks"
CODEX_HOOKS_BIN="$REPO_ROOT/medha-api/bin/agent-mem-codex-hooks"

# ── helpers ────────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
    C_BLUE='\033[0;34m'; C_GREEN='\033[0;32m'
    C_YELLOW='\033[0;33m'; C_RESET='\033[0m'
else
    C_BLUE=''; C_GREEN=''; C_YELLOW=''; C_RESET=''
fi
info() { printf "${C_BLUE}•${C_RESET} %s\n" "$*"; }
ok()   { printf "${C_GREEN}✓${C_RESET} %s\n" "$*"; }
warn() { printf "${C_YELLOW}!${C_RESET} %s\n" "$*"; }
die()  { printf '\033[0;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required but not found"

# ── 1. check binary ────────────────────────────────────────────────────────────
[ -x "$CODEX_HOOKS_BIN" ] || die "Binary not found: $CODEX_HOOKS_BIN\nRun 'make build-codex-hooks' first."

# ── 2. copy binary ─────────────────────────────────────────────────────────────
info "Copying binary → $HOOKS_BIN_DIR/agent-mem-codex-hooks"
mkdir -p "$HOOKS_BIN_DIR"
DST_BIN="$HOOKS_BIN_DIR/agent-mem-codex-hooks"
[ "$CODEX_HOOKS_BIN" -ef "$DST_BIN" ] || cp "$CODEX_HOOKS_BIN" "$DST_BIN"
chmod +x "$DST_BIN"
ok "Binary copied"

# ── 3. write / merge hooks.json ────────────────────────────────────────────────
info "Updating $HOOKS_JSON"
mkdir -p "$CODEX_DIR"

# All 10 Codex hook event names.
ALL_EVENTS='[
  "PreToolUse","PostToolUse","PermissionRequest",
  "SessionStart","Stop","SubagentStart","SubagentStop",
  "PreCompact","PostCompact","UserPromptSubmit"
]'

HOOK_CMD="$DST_BIN"

if [ -f "$HOOKS_JSON" ]; then
    # Merge: add the hook to any event that doesn't already reference this binary.
    MERGED=$(jq \
      --arg cmd "$HOOK_CMD" \
      --argjson events "$ALL_EVENTS" \
      '
      .hooks //= {} |
      reduce $events[] as $event (
        .;
        if .hooks[$event] == null then
          .hooks[$event] = [{ hooks: [{ type: "command", command: $cmd }] }]
        elif ([.hooks[$event][].hooks[]?.command] | index($cmd)) != null then
          .
        else
          .hooks[$event] += [{ hooks: [{ type: "command", command: $cmd }] }]
        end
      )
      ' "$HOOKS_JSON")
    printf '%s\n' "$MERGED" > "$HOOKS_JSON"
else
    jq -n \
      --arg cmd "$HOOK_CMD" \
      --argjson events "$ALL_EVENTS" \
      '{ hooks: (reduce $events[] as $event ({}; .[$event] = [{ hooks: [{ type: "command", command: $cmd }] }])) }' \
      > "$HOOKS_JSON"
fi
ok "hooks.json updated"

echo ""
echo "  Binary:    $DST_BIN"
echo "  Config:    $HOOKS_JSON"
echo ""
warn "Project-local hooks require trust before Codex runs them."
warn "Open Codex, run /hooks, and trust the agent-mem-codex-hooks entries."
