# C2PA Content Credentials Standard Research

_Last updated: 2026-02-18 (UTC)_

## Executive Summary

**Metaphor:** C2PA is a sealed evidence bag attached to media.
It packages a chain of assertions about how an asset was created or edited, then seals it with cryptographic signatures so consumers can verify tampering.

## Standard At A Glance

| Dimension | What C2PA Defines |
|---|---|
| Scope | Provenance and authenticity metadata for digital media |
| Primary artifact | C2PA manifest with claims, assertions, ingredients, signatures |
| Intended producers | Cameras, editors, media pipelines |
| Intended consumers | Viewers, publishers, moderation, verification tools |
| Status | Stable spec with versioned releases |
| Reference implementation | `c2pa-org/specifications` |

## Purpose & Scope

C2PA specifies how to embed provenance assertions into media assets and how to verify them. It is not Git-specific and is focused on media workflows rather than source code, but the core concepts map to provenance: claims, actions, and derivation chains.

## Core Representation

| Concept | Description | Why It Matters |
|---|---|---|
| Manifest | Container for claims and assertions | Primary provenance payload |
| Claim | Core statement about the asset | Establishes the assertion context |
| Assertions | Structured statements like actions and metadata | Describes what happened to the asset |
| Ingredients | Inputs and derivations | Encodes lineage and reuse |
| Signature | Cryptographic seal over the manifest | Enables verification and tamper detection |

## Sample Representation

Synthetic example that mirrors common manifest fields and assertions.

```json
{
  "c2pa": {
    "claim_generator": "example-editor/3.2.1",
    "format": "image/jpeg",
    "instance_id": "urn:uuid:550e8400-e29b-41d4-a716-446655440000",
    "assertions": [
      {
        "label": "c2pa.actions",
        "data": {
          "actions": [
            { "action": "c2pa.created", "when": "2026-01-23T14:00:00Z" },
            { "action": "c2pa.edited", "when": "2026-01-23T14:05:10Z" }
          ]
        }
      },
      {
        "label": "c2pa.hash.data",
        "data": { "alg": "sha256", "hash": "b1946ac92492d2347c6235b4d2611184" }
      }
    ],
    "ingredients": [
      {
        "title": "original.jpg",
        "relationship": "parentOf",
        "digest": { "alg": "sha256", "hash": "a1b2c3d4" }
      }
    ],
    "signature": { "alg": "ES256", "issuer": "Example CA" }
  }
}
```

| Field | Role In Provenance / Blame |
|---|---|
| `assertions` | Encodes creation and edit actions |
| `ingredients` | Captures lineage and upstream assets |
| `signature` | Provides authenticity and tamper detection |

## Capture / Emission Model

Capture is performed by media tools and pipelines that embed C2PA manifests into assets. The spec defines the manifest structure and embedding mechanics, not the editorial workflow itself.

```mermaid
flowchart LR
  A[Editor] --> B[Manifest]
  B --> C[Embed in Asset]
  C --> D[Verifier]
```

## Verification / Trust Model

C2PA uses cryptographic signatures over manifests. Verifiers check signatures and compare embedded hashes with the asset to detect tampering.

## Git / SCM Relevance

C2PA is not Git-specific. Its data model can inspire provenance for code artifacts, but it does not define Git storage or blame semantics.

## Tooling & Ecosystem

| Tool / Project | Role | Notes |
|---|---|---|
| `c2pa-org/specifications` | Specification | Defines manifest structure and embedding |
| Media editors | Producers | Embed C2PA manifests |
| Verification tools | Consumers | Validate signatures and assertions |

## Activity, Support, And Community (as of 2026-02-18 UTC)

| Metric | Value |
|---|---:|
| Stars | 166 |
| Forks | 25 |
| Watchers | 12 |
| Open issues | 28 |
| Last push | 2026-01-14T04:25:02Z |

| Metric | Value |
|---|---:|
| Open PRs | 3 |
| Closed PRs | 42 |
| Top contributors (API snapshot) | `lrosenthol` |

## Delivery Cadence

| Repo | Version | Published (UTC) | Channel |
|---|---|---|---|
| `c2pa-org/specifications` | `2.3` | 2026-01-05 22:44:10Z | Tag |
| `c2pa-org/specifications` | `1.3` | 2023-03-30 15:00:28Z | Tag |
| `c2pa-org/specifications` | `v1.0` | 2021-12-22 19:47:22Z | Tag |

## Observed vs Inferred

| Type | Notes |
|---|---|
| Observed in spec | Manifests contain claims and assertions embedded in assets |
| Observed in spec | Signatures provide authenticity and tamper detection |
| Inferred | Editorial workflows determine what assertions are emitted |

## Sources

- https://github.com/c2pa-org/specifications
- https://spec.c2pa.org/specifications/specifications/2.3/specs/C2PA_Specification.html
- https://spec.c2pa.org/specifications/specifications/2.3/ai-ml/ai_ml.html
