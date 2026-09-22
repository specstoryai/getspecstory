# Agent Trace Standard Research

_Last updated: 2026-02-18 (UTC)_

## Executive Summary

**Metaphor:** Agent Trace is a shipping container spec, not the shipping network.
It standardizes the payload shape for attribution records and a blame lookup recipe, but it does not define capture, storage, or sync behavior.

## Standard At A Glance

| Dimension | What Agent Trace Defines |
|---|---|
| Scope | AI attribution schema + blame lookup guidance |
| Primary artifact | Trace records containing files, ranges, conversations, contributor |
| Intended producers | Agent hooks, editors, capture tools |
| Intended consumers | Blame tooling, provenance viewers |
| Status | Proposal with reference implementation |
| Reference implementation | Hook script + JSONL store |

## Purpose & Scope

Agent Trace defines a vendor-neutral schema for AI attribution so different tools can emit and consume compatible provenance data. It does not mandate how to capture or store that data, nor does it implement a complete AI provenance system.

## Core Representation

| Concept | Description | Why It Matters |
|---|---|---|
| `TraceRecord` | Top-level record with `version`, `id`, `timestamp` | Unique, time-ordered provenance unit |
| `vcs` | VCS info with `type` and optional `revision` | Links attribution to a Git revision and blame |
| `tool` | Emitting tool identity | Indicates which agent produced the data |
| `files[]` | File paths with `conversations[]` | Maps attribution to specific files |
| `ranges[]` | Line ranges and `content_hash` | Enables line-level provenance lookup |
| `contributor` | AI vs human + optional model info | Distinguishes human vs agent work |

## Sample Representation

Synthetic example aligned to the published schema fields.

```json
{
  "version": "0.1.0",
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2026-01-23T14:30:00Z",
  "vcs": { "type": "git", "revision": "a1b2c3d4" },
  "tool": { "name": "cursor", "version": "2.4.0" },
  "files": [
    {
      "path": "src/utils/parser.ts",
      "conversations": [
        {
          "url": "https://api.cursor.com/v1/conversations/12345",
          "contributor": { "type": "ai", "model_id": "anthropic/claude-opus-4-5-20251101" },
          "ranges": [
            { "start_line": 42, "end_line": 67, "content_hash": "murmur3:9f2e8a1b" }
          ]
        }
      ]
    }
  ]
}
```

| Field | Role In Provenance / Blame |
|---|---|
| `vcs.revision` | Links to the commit used by `git blame` |
| `files.path` + `ranges` | Provides line-level attribution targets |
| `contributor` | Signals AI vs human and model identity |
| `conversations.url` | Enables drill-down to transcript data |

## Capture / Emission Model

Capture is implementation-defined. The reference hook maps agent hook events into trace records and appends them to a local JSONL file.

## Verification / Trust Model

Agent Trace does not define signing or attestation. Trust is delegated to the storage and distribution mechanism chosen by implementers.

## Git / SCM Relevance

The schema includes `vcs.revision`, and the spec describes a blame workflow: run VCS blame, locate the corresponding trace record by file and range, and display contributor information.

## Tooling & Ecosystem

| Tool / Project | Role | Notes |
|---|---|---|
| `cursor/agent-trace` | Reference implementation | Hook script + JSONL store |
| `agent-trace.dev` | Spec docs | Schema and workflow overview |
| Third-party hooks | Producers | Any tool that emits the schema |

## Activity, Support, And Community (as of 2026-02-18 UTC)

| Metric | Value |
|---|---:|
| Stars | 578 |
| Forks | 46 |
| Watchers | 15 |
| Open issues | 8 |
| Last push | 2026-02-06T14:23:28Z |

| Metric | Value |
|---|---:|
| Open PRs | 1 |
| Closed PRs | 7 |
| Top contributors (API snapshot) | `leerob`, `cursoragent` |

## Delivery Cadence

| Repo | Version | Published (UTC) | Channel |
|---|---|---|---|
| `cursor/agent-trace` | `-` | `-` | No tagged releases |

## Observed vs Inferred

| Type | Notes |
|---|---|
| Observed in code/docs | Schema definitions in `schemas.ts` and examples in `README.md` |
| Observed in code/docs | Reference hook in `reference/trace-hook.ts` writing JSONL traces |
| Inferred | Most value comes from interoperability, not from storage or sync |

## Sources

- https://agent-trace.dev/
- https://github.com/cursor/agent-trace
- https://github.com/cursor/agent-trace/blob/main/README.md
- https://github.com/cursor/agent-trace/blob/main/schemas.ts
