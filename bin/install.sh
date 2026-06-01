#!/usr/bin/env bash
# Install agent-mem hooks and slash commands into a Claude Code project.
#
# Usage:
#   ./bin/install.sh [TARGET_DIR]
#
# TARGET_DIR defaults to the current working directory.
# Idempotent — safe to re-run to update an existing install.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOURCE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"   # repo root

TARGET="${1:-$(pwd)}"
TARGET="$(cd "$TARGET" && pwd)"

HOOKS_SRC="$SCRIPT_DIR"
CMDS_SRC="$SOURCE_DIR/.claude/commands"

HOOKS_DST="$TARGET/.claude/hooks"
CMDS_DST="$TARGET/.claude/commands"
SETTINGS="$TARGET/.claude/settings.json"

# ── helpers ────────────────────────────────────────────────────────────────
info()  { printf '\033[0;34m• %s\033[0m\n' "$*"; }
ok()    { printf '\033[0;32m✓ %s\033[0m\n' "$*"; }
warn()  { printf '\033[0;33m! %s\033[0m\n' "$*"; }
die()   { printf '\033[0;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

command -v jq >/dev/null 2>&1 || die "jq is required but not found"

# ── 1. copy hook binaries ───────────────────────────────────────────────────
info "Installing hooks → $HOOKS_DST"
mkdir -p "$HOOKS_DST"

for hook in agentmem-tool-hook agentmem-session-end-hook agentmem-notify-hook agentmem-md-procedural-hook agentmem-seed-procedural; do
    src="$HOOKS_SRC/$hook"
    dst="$HOOKS_DST/$hook"
    [ -f "$src" ] || die "Hook not found: $src"
    [ "$src" -ef "$dst" ] || cp "$src" "$dst"
    chmod +x "$dst"
done
ok "Hooks copied"

# ── 2. copy slash commands ──────────────────────────────────────────────────
if [ -d "$CMDS_SRC" ] && ls "$CMDS_SRC"/mem-*.md >/dev/null 2>&1; then
    info "Installing commands → $CMDS_DST"
    mkdir -p "$CMDS_DST"
    for cmd in "$CMDS_SRC"/mem-*.md; do
        dst_cmd="$CMDS_DST/$(basename "$cmd")"
        [ "$cmd" -ef "$dst_cmd" ] || cp "$cmd" "$dst_cmd"
    done
    ok "Commands copied"
else
    warn "No mem-*.md commands found in $CMDS_SRC — skipping"
fi

# ── 3. merge hooks into settings.json ──────────────────────────────────────
info "Updating $SETTINGS"
mkdir -p "$TARGET/.claude"

TOOL_HOOK="$HOOKS_DST/agentmem-tool-hook"
NOTIFY_HOOK="$HOOKS_DST/agentmem-notify-hook"
END_HOOK="$HOOKS_DST/agentmem-session-end-hook"
MD_HOOK="$HOOKS_DST/agentmem-md-procedural-hook"

if [ -f "$SETTINGS" ]; then
    MERGED=$(jq \
      --arg tool   "$TOOL_HOOK"   \
      --arg notify "$NOTIFY_HOOK" \
      --arg end    "$END_HOOK"    \
      --arg md     "$MD_HOOK"     \
      '
      def add_hook(event; entry; cmd):
        if .hooks[event] == null then
          .hooks[event] = [entry]
        elif ([.hooks[event][].hooks[]?.command] | index(cmd)) != null then
          .
        else
          .hooks[event] += [entry]
        end;

      .hooks //= {} |
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
      --arg md     "$MD_HOOK"     \
      '{
        hooks: {
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

ok "settings.json updated"

# ── done ───────────────────────────────────────────────────────────────────
echo ""
echo "agent-mem installed in: $TARGET/.claude/"
echo ""
printf '  Hooks:    '; ls "$HOOKS_DST" | tr '\n' ' '; echo
printf '  Commands: '; ls "$CMDS_DST"/*.md 2>/dev/null | xargs -n1 basename | tr '\n' ' '; echo
echo ""
echo "Restart Claude Code in that directory to pick up the changes."
