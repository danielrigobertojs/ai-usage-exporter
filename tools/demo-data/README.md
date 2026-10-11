# Synthetic dashboard data

Generate the deterministic, non-personal provider data used for the README
visuals with:

```bash
go run ./tools/demo-data -out /tmp/ai-usage-demo
tools/demo-data/capture.sh docs/images
```

The generator only writes invented session IDs, `project-alpha`, and fixed
timestamps. It never reads a user home directory or an existing agent log.
The resulting fixture contains Claude Code JSONL, a Codex rollout JSONL, and
an OpenCode SQLite database at `opencode/opencode.db`, matching the provider's
documented lookup path.
