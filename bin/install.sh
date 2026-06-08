#!/usr/bin/env bash
# Low-level file deployer — copies agent-mem hooks and slash commands into a
# Claude Code project and wires the hook events in settings.json.
#
# Called by bin/connect.sh (user setup) and medha_dev_setup.sh (dev setup).
# Does not prompt, does not write .env.mcp, does not register MCP.
#
# Usage:
#   ./bin/install.sh [TARGET_DIR]
#
# TARGET_DIR defaults to the current working directory.
# Idempotent — safe to re-run to update an existing install.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

TARGET="${1:-$(pwd)}"
TARGET="$(cd "$TARGET" && pwd)"

HOOKS_SRC="$SCRIPT_DIR"
CMDS_SRC="$SCRIPT_DIR/commands"

HOOKS_DST="$TARGET/.claude/hooks"
CMDS_DST="$TARGET/.claude/commands"
SETTINGS="$TARGET/.claude/settings.json"

# ── helpers ────────────────────────────────────────────────────────────────
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

# ── 1. copy hook binaries ───────────────────────────────────────────────────
info "Copying hooks → $HOOKS_DST"
mkdir -p "$HOOKS_DST"

for hook in agentmem-tool-hook agentmem-session-end-hook agentmem-notify-hook \
            agentmem-recall-hook agentmem-md-procedural-hook agentmem-seed-procedural; do
    src="$HOOKS_SRC/$hook"
    dst="$HOOKS_DST/$hook"
    [ -f "$src" ] || die "Hook not found: $src"
    [ "$src" -ef "$dst" ] || cp "$src" "$dst"
    chmod +x "$dst"
done
ok "Hooks copied"

# ── 2. copy slash commands ──────────────────────────────────────────────────
if [ -d "$CMDS_SRC" ] && ls "$CMDS_SRC"/*.md >/dev/null 2>&1; then
    info "Copying commands → $CMDS_DST"
    mkdir -p "$CMDS_DST"
    for cmd in "$CMDS_SRC"/*.md; do
        dst_cmd="$CMDS_DST/$(basename "$cmd")"
        [ "$cmd" -ef "$dst_cmd" ] || cp "$cmd" "$dst_cmd"
    done
    ok "Commands copied"
else
    warn "No *.md commands found in $CMDS_SRC — skipping"
fi

# ── 3. merge hooks into settings.json ──────────────────────────────────────
info "Updating $SETTINGS"
mkdir -p "$TARGET/.claude"

TOOL_HOOK="$HOOKS_DST/agentmem-tool-hook"
NOTIFY_HOOK="$HOOKS_DST/agentmem-notify-hook"
END_HOOK="$HOOKS_DST/agentmem-session-end-hook"
RECALL_HOOK="$HOOKS_DST/agentmem-recall-hook"
MD_HOOK="$HOOKS_DST/agentmem-md-procedural-hook"

# Legacy script names that install.sh used to deploy — removed, keep settings clean.
LEGACY_HOOKS=("recall-memories.sh" "observe-tool.sh" "notify.sh" "session-end.sh")

if [ -f "$SETTINGS" ]; then
    # Strip legacy hook entries then add/idempotently merge current hooks.
    MERGED=$(jq \
      --arg tool   "$TOOL_HOOK"   \
      --arg notify "$NOTIFY_HOOK" \
      --arg end    "$END_HOOK"    \
      --arg recall "$RECALL_HOOK" \
      --arg md     "$MD_HOOK"     \
      --argjson legacy '["recall-memories.sh","observe-tool.sh","notify.sh","session-end.sh"]' \
      '
      def is_legacy: . as $cmd | $legacy | any(.[]; $cmd | endswith(.));

      def strip_legacy: [
        .[] |
        .hooks = [.hooks[] | select((.command // "") | is_legacy | not)] |
        select(.hooks | length > 0)
      ];

      def add_hook(event; entry; cmd):
        if .hooks[event] == null then
          .hooks[event] = [entry]
        elif ([.hooks[event][].hooks[]?.command] | index(cmd)) != null then
          .
        else
          .hooks[event] += [entry]
        end;

      .hooks //= {} |
      .hooks |= with_entries(.value = (.value | strip_legacy)) |
      add_hook("UserPromptSubmit";
        { hooks: [{ type: "command", command: $recall }] };
        $recall) |
      add_hook("UserPromptExpansion";
        { hooks: [{ type: "command", command: $recall }] };
        $recall) |
      add_hook("PostToolUse";
        { matcher: "Bash|Edit|Write|Read|WebSearch|WebFetch|Agent",
          hooks: [{ type: "command", command: $tool }] };
        $tool) |
      add_hook("PostToolUse";
        { matcher: "Edit|Write",
          hooks: [{ type: "command", command: $md }] };
        $md) |
      add_hook("Notification";
        { hooks: [{ type: "command", command: $notify }] };
        $notify) |
      add_hook("Stop";
        { hooks: [{ type: "command", command: $end }] };
        $end)
      ' "$SETTINGS")
    printf '%s\n' "$MERGED" > "$SETTINGS"
else
    jq -n \
      --arg tool   "$TOOL_HOOK"   \
      --arg notify "$NOTIFY_HOOK" \
      --arg end    "$END_HOOK"    \
      --arg recall "$RECALL_HOOK" \
      --arg md     "$MD_HOOK"     \
      '{
        hooks: {
          UserPromptSubmit: [
            { hooks: [{ type: "command", command: $recall }] }
          ],
          UserPromptExpansion: [
            { hooks: [{ type: "command", command: $recall }] }
          ],
          PostToolUse: [
            { matcher: "Bash|Edit|Write|Read|WebSearch|WebFetch|Agent",
              hooks: [{ type: "command", command: $tool }] },
            { matcher: "Edit|Write",
              hooks: [{ type: "command", command: $md }] }
          ],
          Notification: [
            { hooks: [{ type: "command", command: $notify }] }
          ],
          Stop: [
            { hooks: [{ type: "command", command: $end }] }
          ]
        }
      }' > "$SETTINGS"
fi

# Remove legacy script files from the hooks directory.
for legacy in "${LEGACY_HOOKS[@]}"; do
    legacy_path="$HOOKS_DST/$legacy"
    if [ -f "$legacy_path" ]; then
        rm "$legacy_path"
        warn "Removed legacy hook: $legacy"
    fi
done
ok "settings.json updated"

echo ""
printf '  Hooks:    '; ls "$HOOKS_DST" | tr '\n' ' '; echo
printf '  Commands: '; ls "$CMDS_DST"/*.md 2>/dev/null | xargs -n1 basename | tr '\n' ' ' || true; echo
