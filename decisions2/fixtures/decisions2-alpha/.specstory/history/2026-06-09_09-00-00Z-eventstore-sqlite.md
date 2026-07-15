## 2026-06-09 09:00:00Z

<!-- Claude Code Session aaaaaaaa-0000-4000-8000-000000000002 (2026-06-09_09-00-00Z) -->

_**User**_

actually, Postgres is overkill here. let's switch the EventStore to SQLite instead

---

_**Agent (claude-opus-4-20250514)**_

Tool use: **Bash**

```bash
git add src/EventStore.ts && git commit -m "$(cat <<'EOF'
refactor(EventStore): switch persistence from Postgres to SQLite

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```
