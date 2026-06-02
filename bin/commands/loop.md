Run one memory-aware loop iteration. Medha provides context at the start and records progress at the end so state survives across iterations and sessions.

**Setup (first call or when $ARGUMENTS is non-empty):**
1. Call mcp__agent-mem__slot-get with slotName="loop:state" to check for an in-progress loop.
2. If no persisted state exists and $ARGUMENTS is non-empty, call mcp__agent-mem__action-create with title="Loop: $ARGUMENTS" and status="pending" to register the goal. Save the returned action ID.
3. Call mcp__agent-mem__get-context with query="$ARGUMENTS", includeLongTerm=true, includeShortTerm=true, includeSlots=true to load relevant past memories, preferences, and pinned context. Use this as background for the iteration.

**Iteration:**
4. Perform one concrete unit of work toward the loop goal, informed by the context from step 3 and any persisted state from step 1.

**Wrap-up (after each iteration):**
5. Call mcp__agent-mem__slot-set with slotName="loop:state" and content summarising: what was done this iteration, what remains, and any key findings. This persists state for the next iteration.
6. If the goal is complete:
   - Call mcp__agent-mem__action-update with status="completed" on the action from step 2.
   - Call mcp__agent-mem__slot-set with slotName="loop:state" and content="" to clear loop state.
   - Call mcp__agent-mem__remember with a summary of the overall outcome as content, type="fact".
   - Report: done, what was accomplished.
7. If more iterations are needed, report: what was done this iteration, what remains, and that the next `/loop` call will continue.

Arguments: $ARGUMENTS
