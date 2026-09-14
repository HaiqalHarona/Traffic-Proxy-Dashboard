---
trigger: model_decision
description: Enforces file naming conventions and cross-linking rules for project documentation.
---

# Documentation Standards

- All markdown files placed inside the `doc/` directory MUST use ALL CAPS with underscores if needed (e.g., `DEVELOPER_GUIDE.md`, `PIPELINE.md`, `USER_GUIDE.md`, `UPDATE.md`).
- When introducing or altering files, directories, or core architectural components, update `doc/DEVELOPER_GUIDE.md` to reflect the changes.
- Ensure that `README.md` maintains valid relative markdown links to documentation in `doc/`.
