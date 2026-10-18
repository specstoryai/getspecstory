# Project Identity

Every project the CLI touches gets a stable identity, written to
`.specstory/.project.json`. It is what the cloud groups sessions by, what
`sessions.db` stores in `project_id`, and what lets a project survive being
moved, renamed, or re-cloned.

The file is created alongside the `.specstory` directory and is only filled in
where values are missing — an existing file is never rewritten wholesale. In
particular, if the file exists but has no `git_id`, each run re-checks whether
one can be derived now (a project that gained a git remote after its first
session picks one up).

Implementation: `pkg/utils/project_identity.go`.

## The file

```json
{
  "workspace_id": "a1b2-c3d4-e5f6-7890",
  "workspace_id_at": "2026-09-15T10:57:00Z",
  "git_id": "1234-5678-9abc-def0",
  "git_id_at": "2026-09-15T10:57:00Z",
  "project_name": "langchainrb"
}
```

| Key               | Always present | Meaning                                                      |
| ----------------- | -------------- | ------------------------------------------------------------ |
| `workspace_id`    | yes            | Path-derived identity                                        |
| `workspace_id_at` | yes            | ISO 8601 timestamp it was assigned                           |
| `git_id`          | no             | Remote-derived identity; absent when there is no git origin   |
| `git_id_at`       | no             | ISO 8601 timestamp it was assigned                           |
| `project_name`    | no             | Human-readable name — the repo name from the origin URL, else the directory basename |

Both IDs are the **first 16 hex characters of a SHA-256**, dash-grouped into
`xxxx-xxxx-xxxx-xxxx` (`createHash`). The format matches the TypeScript
implementation the extension used, so IDs are interchangeable between them.

`GetProjectID()` returns the `git_id` when there is one and falls back to
`workspace_id`.

## `git_id` — derived from the origin remote

Read from the `origin` remote in `.git/config`, normalized, then hashed. Identity
resolution **walks up** from the project directory to the enclosing git root
(`findGitRoot`), so running an agent from a subdirectory of a repo still resolves
to the repo's identity rather than minting a new one per subdirectory.

Normalization (`normalizeGitURL`) is **host-agnostic** — it is not a GitHub
special case. It strips a trailing `.git`, rewrites any `user@host:path` into
`host/path`, and strips any `scheme://` prefix. So every one of these hashes
identically:

| Origin URL                                             | Normalizes to                            |
| ------------------------------------------------------ | ---------------------------------------- |
| `https://github.com/patterns-ai-core/langchainrb.git`   | `github.com/patterns-ai-core/langchainrb` |
| `git@github.com:patterns-ai-core/langchainrb.git`       | `github.com/patterns-ai-core/langchainrb` |
| `github.com/patterns-ai-core/langchainrb`               | `github.com/patterns-ai-core/langchainrb` |

and the same collapsing applies to any other host — `git@gitlab.com:owner/repo`
and `https://gitlab.com/owner/repo` both normalize to `gitlab.com/owner/repo`.
Because the host is retained, two different forges with the same owner/repo path
do *not* collide.

The practical consequence: **`git_id` is stable across clones, machines, and
users**, so it is the join key for cross-project and cross-machine resume.

## `workspace_id` — derived from the path

A hash of the project's absolute path. On **case-insensitive filesystems**
(macOS, Windows) the path is lowercased first (`canonicalizeWorkspacePath`), so
`/Users/me/Repo` and `/Users/me/repo` — one physical directory — produce one ID.
On case-sensitive filesystems the path is hashed byte-exact, because there those
genuinely are two directories.

## Overrides

Two hidden root-level flags let a caller (in practice the VS Code extension,
driving a remote workspace) supply identity inputs the CLI cannot read for
itself. See [EXTENSION-ARCHITECTURE.md](EXTENSION-ARCHITECTURE.md).

| Flag                   | Effect                                                                 |
| ---------------------- | ---------------------------------------------------------------------- |
| `--project-path <path>` | Identity describes *this* project rather than the process cwd. Detection walks up from here, and `project_name` becomes its basename. |
| `--git-origin <url>`    | Use this origin URL instead of reading `.git/config`. Applied even when a `git_id` already exists. |

When `--project-path` names a path that is not present on this machine (an SSH
remote path), it is hashed exactly as given: no walk-up (the local ancestors
could hold an unrelated repo's `.git`), no `filepath.Abs` (it would staple on the
current drive letter), and no case-folding (the remote filesystem is likely
case-sensitive even if the host is not). Git identity for such targets has to
come from `--git-origin`.

## Why two IDs

The goal is project persistence that survives ordinary user behavior.

`workspace_id` alone survives the project directory being moved or renamed,
because it was persisted into `.specstory/.project.json` and is read back rather
than recomputed.

`git_id` is more resilient still, and covers cases `workspace_id` cannot:

- the user deletes `.specstory/`
- the user moves or renames the workspace *and* deletes `.specstory/`
- the same project exists on several machines, or several places on one machine
- several users have the same project on their own machines

Both are defeated by:

- no git, and the user moves/renames the workspace and deletes `.specstory/`
- git, but the user changes the origin remote and deletes `.specstory/`

In those cases the project is genuinely unrecoverable from data alone; re-identifying
it would need to involve the user.

## Related

- [SESSIONS-DB.md](SESSIONS-DB.md) — how `project_id` is used by the session index,
  including `ComputeProjectID`, which resolves identity without writing a
  `.project.json`.
- [EXTENSION-ARCHITECTURE.md](EXTENSION-ARCHITECTURE.md) — why the overrides exist.
