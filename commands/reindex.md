---
name: reindex
description: Rebuild the max-context index (run max-context --reindex).
---

Run a full reindex of the codebase so query_codebase and get_architecture use up-to-date data.

Execute in the project root:

```bash
max-context --reindex
```

If the max-context MCP server is running (e.g. via Claude Code), you can alternatively trigger a reindex by writing to the queue file:

```bash
touch .max-context/.reindex-queue
```

Tell the user when the reindex has been triggered and that they can continue once it completes.
