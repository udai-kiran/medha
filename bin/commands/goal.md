Set or inspect a goal. The medha harness hooks will automatically record your progress, observe tool use, and consolidate memory at session end — you don't need to do anything extra.

Parse $ARGUMENTS:

- **Empty or "list":** Call mcp__agent-mem__frontier to show all ready goals. Display as a numbered list with ID and title.
- **"next":** Call mcp__agent-mem__next-action. Display the returned goal and start working toward it.
- **"done <id>":** Call mcp__agent-mem__action-update with status="completed" for that action ID. Confirm.
- **Any other text:** Call mcp__agent-mem__action-create with the text as `title` and status="pending". Then call mcp__agent-mem__get-context with the text as `query` to surface relevant past memories as background. Start working — the hooks will take it from here.

Arguments: $ARGUMENTS
