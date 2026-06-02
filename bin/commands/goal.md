Manage goals using the medha action system. Parse $ARGUMENTS as follows:

**No argument or "list":** Call mcp__agent-mem__frontier (project = current project if known) to list all ready goals. Display them as a numbered list with ID, title, and status. If the frontier is empty, say so.

**"next":** Call mcp__agent-mem__next-action to get the single highest-priority unblocked goal. Display its title, description, and ID.

**"done <id>":** Extract the action ID from $ARGUMENTS. Call mcp__agent-mem__action-update with that ID and status="completed". Confirm with the goal title.

**"crystallize <id1> <id2> ...":** Extract the IDs. Call mcp__agent-mem__crystallize with those actionIds to compact them into a single summary action. Show the crystallized action ID and title.

**Any other text:** Treat $ARGUMENTS as a new goal. Call mcp__agent-mem__action-create with the text as `title`, status="pending". Show the new action ID. Then call mcp__agent-mem__remember with the same text as content, type="project", so the goal also enters long-term memory.

Arguments: $ARGUMENTS
