Start or continue a loop. The medha harness hooks handle memory automatically — the recall hook injects relevant context at each prompt, the tool hook records every action, and the session-end hook consolidates what was learned. You only need to set intent here.

1. If $ARGUMENTS is non-empty: call mcp__agent-mem__action-create with title="Loop: $ARGUMENTS" and status="pending" to register the goal. Then call mcp__agent-mem__get-context with query="$ARGUMENTS" to load relevant past context before the first iteration begins.
2. If $ARGUMENTS is empty: call mcp__agent-mem__slot-get with slotName="loop:state" to resume from where the last iteration left off.
3. Do the work. The hooks observe everything.
4. When the loop goal is complete, call mcp__agent-mem__action-update with status="completed" on the action from step 1.

Arguments: $ARGUMENTS
