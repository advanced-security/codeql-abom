---
description: "Use before changing any code in codeql-abom. Points Copilot to the Knowledge Base routing index so it opens only the generated knowledge relevant to the task, and treats the code as authoritative when it disagrees with that knowledge."
applyTo: "**"
---

<!-- github-knowledge-base-start -->
## Knowledge Base

### Purpose

This repository uses the Knowledge Base at [https://github.com/advanced-security/codeql-abom](https://github.com/advanced-security/codeql-abom) on branch `main`.

### Required behavior

1. Before changing code, read `docs/index.md` from that branch.
2. Use the index to open only the knowledge files relevant to the task.
3. If the index is unavailable, stop and report that the Knowledge Base could not be loaded.

### Source of truth

Generated knowledge tracks the code. When the knowledge and code disagree, trust the code.
<!-- github-knowledge-base-end -->
