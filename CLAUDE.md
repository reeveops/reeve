# CLAUDE.md

Claude Code reads this file, not `AGENTS.md`. The line below is an import
directive that inlines `AGENTS.md` into context at session start.

@AGENTS.md

Keep project instructions in `AGENTS.md` so Codex, Cursor, Copilot, Gemini
CLI, and CodeRabbit read the same source. Add Claude-specific instructions
below this line only if they do not apply to other agents.