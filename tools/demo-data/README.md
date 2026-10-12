# Synthetic demo data

Generate deterministic, non-personal provider data for manual demos and tests
with:

```bash
go run ./tools/demo-data -out /tmp/ai-usage-demo
```

The generator only writes invented session IDs, `project-alpha`, and fixed
timestamps. It never reads a user home directory or an existing agent log.
The resulting fixture contains Claude Code JSONL, a Codex rollout JSONL, and
an OpenCode SQLite database at `opencode/opencode.db`, matching the provider's
documented lookup path.
