# AI Provenance Representation Standards Summary (6 Standards)

This summary is intentionally scoped to **Challenge 4: Representing Agent Provenance**.  

It does **not** evaluate capture, storage, sync, or blame UX mechanics except where those constraints directly affect representation design.

## What A Representation Must Encode (SpecStory Lens)

For this research task, a strong provenance representation should support:

1. File + line range linkage
2. AI vs human actor identity
3. Prompt linkage (user input that led to a change)
4. Tool-use linkage (which tool edited what)
5. Assistant context linkage (pre/post commentary around tool use)
6. Commit/revision anchor
7. Optional integrity/signature metadata for trust

## Big Picture: Three Representation Families

1. **AI-attribution schema**: Agent Trace (closest to line-level coding provenance payloads)
2. **General provenance model**: W3C PROV (very expressive graph model, not coding-specific)
3. **Attestation/telemetry framing**: in-toto, SLSA, C2PA, OTel GenAI (strong for envelopes, trust, or live telemetry, weaker as direct line-level code attribution schemas)

## Representation Model Comparison

### Core Representation Unit

This compares the primary data structure each standard uses to carry provenance. The core unit shape determines how much modeling work is needed before coding-agent provenance can be represented clearly.

| Standard            | Core Representation Unit                              |
|---------------------|-------------------------------------------------------|
| Agent Trace         | `TraceRecord` with `files[].conversations[].ranges[]` |
| W3C PROV            | Entity-Activity-Agent graph + relations               |
| OpenTelemetry GenAI | Spans/events with `gen_ai.*` attributes               |
| in-toto Statement   | Envelope: `subject[]` + typed `predicate`             |
| SLSA Provenance     | in-toto predicate for build provenance                |
| C2PA                | Signed manifest with assertions/ingredients           |

### Native Line-Range Semantics

Line-level attribution is central to code provenance. This section shows whether line ranges are native in the standard or require an additional custom schema layer.

| Standard            | Native Line-Range Semantics            |
|---------------------|----------------------------------------|
| Agent Trace         | **Strong**                             |
| W3C PROV            | Partial (must be modeled)              |
| OpenTelemetry GenAI | Weak (not line-oriented)               |
| in-toto Statement   | Partial (possible in custom predicate) |
| SLSA Provenance     | Weak for source-line attribution       |
| C2PA                | Weak for source-line attribution       |

### Prompt/Tool/Assistant Semantics

These fields provide the explanatory context behind a change. This section compares how directly each standard can represent prompts, tool activity, and assistant responses around an edit.

| Standard            | Prompt/Tool/Assistant Semantics                                                |
|---------------------|--------------------------------------------------------------------------------|
| Agent Trace         | **Strong** for conversation linkage; tool details are implementation-dependent |
| W3C PROV            | Partial (must define domain mapping)                                           |
| OpenTelemetry GenAI | Partial to strong for request/response/tool telemetry                          |
| in-toto Statement   | Partial (possible in custom predicate)                                         |
| SLSA Provenance     | Weak for prompt/tool/dialogue semantics                                        |
| C2PA                | Weak for coding prompt/tool semantics                                          |

### AI vs Human Actor Semantics

Separating AI-originated changes from human changes is foundational for AI blame. This section compares whether that distinction is explicit or must be inferred through local conventions.

| Standard            | AI vs Human Actor Semantics                                |
|---------------------|------------------------------------------------------------|
| Agent Trace         | **Strong** (`contributor.type`)                            |
| W3C PROV            | **Strong** (agent typing)                                  |
| OpenTelemetry GenAI | Partial (provider/model fields; actor role usually custom) |
| in-toto Statement   | Partial (possible in predicate)                            |
| SLSA Provenance     | Weak for coding-session actor semantics                    |
| C2PA                | Partial for producer/claim agent identity                  |

### Revision Binding

A provenance representation needs to anchor to a stable revision identity. This section compares how naturally each standard binds records to commit or artifact identities used in later lookup workflows.

| Standard            | Revision Binding                                                    |
|---------------------|---------------------------------------------------------------------|
| Agent Trace         | **Strong** (`vcs.revision`)                                         |
| W3C PROV            | Partial (must model Git entities/activities)                        |
| OpenTelemetry GenAI | Weak (Git revision not native)                                      |
| in-toto Statement   | **Strong** at artifact identity; Git commit can be embedded         |
| SLSA Provenance     | **Strong** for build input/dependency revision binding              |
| C2PA                | Partial (can reference source lineage, not Git-native line mapping) |

## Field-Level Fit To Challenge 4 Requirements

Legend: `High` = native/first-class, `Medium` = possible with extensions, `Low` = unnatural fit.

| Standard            | File+Line | Prompt | Tool Use | Assistant Context | AI/Human Distinction | Integrity/Signature      |
|---------------------|-----------|--------|----------|-------------------|----------------------|--------------------------|
| Agent Trace         | High      | High   | Medium   | High              | High                 | Low                      |
| W3C PROV            | Medium    | Medium | Medium   | Medium            | High                 | Low                      |
| OpenTelemetry GenAI | Low       | Medium | Medium   | Medium            | Medium               | Low                      |
| in-toto Statement   | Medium    | Medium | Medium   | Medium            | Medium               | High (with DSSE/signing) |
| SLSA Provenance     | Low       | Low    | Low      | Low               | Low                  | High                     |
| C2PA                | Low       | Low    | Low      | Low               | Medium               | High                     |

## Most Important Trade-offs

### 1) Coding-Specific Fidelity vs General Interoperability

- **Agent Trace** gives the cleanest coding-provenance shape out of the box.
- **W3C PROV** is more universal but requires substantial mapping discipline to avoid ambiguous implementations.

### 2) Rich Attribution Semantics vs Cryptographic Trust

- **Agent Trace / W3C PROV / OTel** are better at expressing activity context.
- **in-toto / SLSA / C2PA** are better at verifiable integrity and trust chains.
- No single candidate is best at both without layering.

### 3) Event Telemetry vs Durable Attribution Records

- **OpenTelemetry GenAI** is excellent for live operation telemetry vocabulary.
- It is not, by itself, a durable line-level provenance representation format for Git history.

### 4) Schema Simplicity vs Extension Burden

- **Agent Trace**: low extension burden for AI coding attribution.
- **in-toto/SLSA**: high extension burden for coding-line semantics, but strong trust ecosystem once modeled.

## Executive Takeaways (Challenge 4 Only)

1. **Best direct fit for AI coding provenance representation:** Agent Trace.
2. **Best general conceptual model for cross-domain provenance graphs:** W3C PROV.
3. **Best trust envelope for signed provenance artifacts:** in-toto (with optional SLSA where build provenance is relevant).
4. **Best complementary telemetry vocabulary:** OpenTelemetry GenAI (supporting input, not final representation).
5. **Lowest fit for direct source-code line-level representation:** SLSA and C2PA (strong elsewhere, not this target).

Bottom line: for Challenge 4, the strongest path is a layered representation strategy: a coding-native attribution schema (Agent Trace-like), optionally projected to PROV, and optionally signed/enveloped with in-toto for verification.
