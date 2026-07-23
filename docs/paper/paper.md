# DeFuzz: Agentic Discovery of Silently-Failing Compiler Defenses via Cross-Mechanism Security Invariants

> **Draft status.** Working draft for an IEEE S&P submission. Body language: English; section numbering follows the S&P convention (Roman numerals). Figures are inserted as placeholders `[[FIG:stem]]` and rendered later. Numbers tagged `[TBD]` depend on the final experimental run; corpus numbers are already grounded in the project archive.
>
> **Working title alternatives.**
> - *A1.* When the Last Line Fails Silently: Invariant-Grounded Agentic Testing of Compiler Defenses
> - *A2.* DeFuzz: Finding Silent Defense Failures Across the Mechanism × ISA Matrix

---

## Abstract

Compiler-inserted defenses—stack canaries, `_FORTIFY_SOURCE`, CET/IBT, CFI, BTI, PAC, and shadow stacks—are the last line that must hold once a memory-safety bug is triggered. Their effectiveness depends not only on the correctness of the defense code but also on assumptions about the underlying instruction set architecture (ISA). When such an assumption breaks on a particular backend, the defense can fail *silently*: the program compiles without warning and runs with correct functionality, yet its security contract is no longer enforced. CVE-2023-4039, in which GCC's stack protector is defeated on AArch64 in the presence of variable-length arrays, is one such case, and its cross-architecture siblings have persisted upstream for years.

Detecting silent failures is hard for two coupled reasons. The search space is a two-dimensional matrix of defense mechanism × ISA, each cell backed by an independent middle-end pass or backend template. And the failure is difficult to adjudicate: the binary neither crashes nor disagrees with other compilers, so crash-based and differential-oracle fuzzers register nothing. We call this the *oracle gap*.

We present DeFuzz, an agentic system that closes the oracle gap and searches the matrix directly. First, we systematize the safety invariants that each defense must satisfy—distilled from compiler source comments, ABI specifications, and confirmed historical bugs—into a machine-checkable oracle. Every checker returns a four-state verdict and stays sound (zero false positives) by grounding each bug claim in a deterministic binary or runtime signal rather than in model output. Second, we introduce a cross-mechanism invariant-generation pipeline that treats a confirmed root cause in one mechanism as a probe, retrieves structurally analogous code in another mechanism, and instantiates a new invariant behind two static grounding gates. From 25 confirmed defense bugs and 24 probes over GCC 16.1, the pipeline yields 11 new cross-mechanism invariants, none of which duplicate an existing seed. Third, we build an explicitly-orchestrated agentic loop: a deterministic pipeline drives build, coverage, and oracle in a fixed order and invokes agents only at fixed positions, where they communicate through a versioned blackboard rather than free-form dialogue. Unlike free-running agent systems, this design yields reproducible traces, per-edge ablation, and bug claims that trace back to deterministic evidence; a checker-bound ISA routing scheme collapses the mechanism × ISA Cartesian product into a single semantic decision the agent is actually good at.

On GCC and LLVM, DeFuzz discovered `[TBD]` silent-failure defects, of which `[TBD]` have been confirmed upstream `[TBD: CVE IDs]`. An ablation shows that oracle grounding, coverage feedback, and each inter-agent edge contribute measurably to hit rate, and that fixing a blackboard version reproduces a run's trajectory exactly.

---

## I. Introduction

*Maps to: story_line.md §1–2; open-topic ch1. Mirrors PropertyGPT §I (gap-driven) and AgentFuzz §1.*

- **Defenses as the last line.** CVE overload; industry concedes upper-layer memory bugs cannot be fully eliminated; compiler/runtime defenses are the backstop. `[[FIG:defense-role]]`
- **Silent (double-silent) failure.** Compiles clean, runs correct, but the security contract is broken. Motivating example CVE-2023-4039 and its under-fixed sibling PR-96191. `[[FIG:stack-layout]]`
- **Two coupled challenges.** (1) Search space = mechanism × ISA matrix; (2) oracle gap—crash/differential oracles are blind to silent logic failure. `[[FIG:defense-matrix]]`
- **Why prior automation falls short.** Static analysis needs known patterns; Csmith/YARPGen-style differential fuzzers and crash fuzzers lack the oracle; free-running LLM-agent fuzzers sacrifice reproducibility.
- **Our approach in one paragraph.** Invariant-grounded oracle + explicitly-orchestrated agentic loop with checker-bound ISA routing.
- **Contributions (four).**
  1. *Oracle for silent failure (main line).* A sound, machine-checkable oracle that fills the silent-failure oracle gap and supplies false-positive-free ground truth for agentic bug hunting.
  2. *Systematized security invariants (support).* A bottom-up taxonomy over 468 catalogued invariants across more than two dozen defenses, each expressed in a machine-decidable static/dynamic form; plus a cross-mechanism generation pipeline (creativity point A).
  3. *Explicitly-orchestrated agent system (moat).* Deterministic pipeline + agents at fixed positions + blackboard linkage → reproducible / ablatable / auditable, in contrast to free-running systems (FuzzAgent, Claude Code).
  4. *Agentic loop (vehicle).* Oracle-grounded, coverage-guided seed generation with checker-bound ISA routing that collapses the Cartesian product.

## II. Background and Motivation

*Maps to: ch1-background.txt; story_line.md §1–3.*

### II-A. Compiler-inserted defenses and the mechanism × ISA matrix
- How compiler defenses work (compile-time insertion of checks/layout/metadata; canary example).
- Effectiveness is ISA-dependent; growing ISA count widens the attack surface. `[[FIG:defense-matrix]]`

### II-B. Silent failure: definition and a real case
- Define *silent failure*: complete-looking defenses that an attacker bypasses; "double-silent" (compile-time + run-time).
- Walk through CVE-2023-4039 buggy stack frame; PR-96191 partial fix leaving fallback backends exposed. `[[FIG:stack-layout]]`

### II-C. Why existing methods miss it
- Static analysis: pattern-bound, cannot cover heterogeneous violation forms.
- Crash/differential fuzzing: surface signals; the oracle gap.
- Free-running agent fuzzers: can find bugs but non-reproducible, non-ablatable, non-auditable.

### II-D. Scope and threat model
- What counts as a silent failure here; falsification surface = describe the violation only, no exploit construction (project constraint).
- Boundaries: GCC/LLVM as targets; source-comment/RAG corpus pinned to GCC 16.1.

## III. Overview

*Maps to: overview.md; agentic-loop-redesign.md §3. Mirrors PropertyGPT §III and AgentFuzz §4.*

- One-paragraph system definition: enumerate checker catalog → per-invariant Generator seeds → routing→build→coverage→oracle deterministic pipeline → verdict → minimize / feedback.
- Two-part architecture: Go deterministic core (gRPC + MCP dual adapter) + Python LangGraph orchestrator. `[[FIG:architecture]]`
- End-to-end walk-through of a single iteration, forward-referencing §IV–VII. `[[FIG:main-loop]]`

## IV. Security Invariants for Compiler Defenses

*Maps to: invariants/README.md; gcc-llvm-defense-invariant-source-survey.md. Mirrors PropertyGPT §IV (PSL) + §VIII property clustering — this is the "survey output" half.*

### IV-A. What is a security invariant
- Definition: a property a defense *must* satisfy at runtime; violation = silent weakening regardless of code bug.
- The `observation` field: externally observable phenomenon only, not the detection recipe.

### IV-B. Survey methodology and sources
- Three source classes: source comments/assertions, compiler & ABI docs, confirmed historical bugs/patches.
- Unified record schema (ID / statement / compiler / version / target / source_kind / evidence / version_sensitivity / observation).

### IV-C. A bottom-up taxonomy
- Cluster 468 invariants data-first (avoid AI-preset bias); present the root-cause families. `[[FIG:invariant-taxonomy]]`
- Illustrative families (from the cross-mechanism archive): exit-time sensitive-register/state residue; stack-clash frame-size truncation; RISC-V large-address materialization sign-extension.

### IV-D. Machine-checkable form: static vs dynamic falsification
- Static (inspect un-executed binary: symbols, sections, disasm) vs dynamic (run + observe). Selection dimensions: observability / decisiveness / cost / static-dynamic attribution.

## V. Cross-Mechanism Invariant Generation (SpecGen)

*Maps to: cross-mechanism-generated.md; cross-mechanism-bm25-vs-embedding.md; project_memory (creativity point A). Mirrors PropertyGPT §V (retrieval-augmented generation + refinement).*

### V-A. Motivation: abstract-failure-mode-driven transfer
- Seed-pool scarcity; why entity-driven (API-symbol) retrieval is rejected in favor of abstract-failure-mode transfer.
- Goal: use a confirmed root cause as a probe to find isomorphic sites in *other* mechanisms.

### V-B. Pipeline: distill → analogy → specialize → entailment
- Distill (mechanism-agnostic root cause) → analogy (semantic isomorphism gate) → specialize (instantiate on target source) → entailment. `[[FIG:specgen-pipeline]]`
- Analogy gate is essential: ~94% of BM25 hits are lexical collisions and must be filtered semantically.

### V-C. Retrieval: BM25 + embedding complementarity
- BM25 (precise anchors on distinctive identifiers) vs `doubao-embedding-vision` (semantic completion); union-dedup.
- Numbers: 24 probes over 4,496 GCC-16.1 chunks → 5 intersection (robust) + 4 BM25-only + 2 embedding-only = 11 union invariants; all `is_novel`. `[[FIG:bm25-vs-embedding]]`

### V-D. Static grounding gates and reproducibility
- Two grounding gates before acceptance; novelty threshold (85.0); query-vector caching to make dense retrieval reproducible.

## VI. Oracle: From Invariants to Verdicts

*Maps to: oracle-mechanism-framework.md; agentic-loop-redesign.md §5; ch3 objective (2). Mirrors PropertyGPT §VI (dedicated verification).*

### VI-A. Checker design
- Four-state verdict; `NotApplicable` transparency; static vs dynamic checkers.
- Soundness: a bug is reported only when a deterministic checker fails or an execution differential reproduces. `[[FIG:oracle-flow]]`

### VI-B. Mechanism aggregator and flag profiles
- Per-mechanism aggregator: any Fail ⇒ mechanism-level violation with evidence.
- On/off flag profiles for differential adjudication; decoupled from the producing compiler (GCC/LLVM share checkers).

### VI-C. Checker metadata as SSOT
- Declarative fields: ISA binding, single-ISA vs differential, cheap vs expensive. Single source of truth read by both gRPC and MCP adapters.

## VII. Agentic Loop

*Maps to: agentic-loop-redesign.md §1.5, §3, §4; overview.md §3. Mirrors AgentFuzz §5 (design → feedback → mutation).*

### VII-A. Design principles
- Deterministic, hard-wired pipeline (generate→build→coverage→oracle→route); agents at fixed positions only; agents linked via shared state, not direct calls; correctness held by deterministic evidence.

### VII-B. Explicit orchestration and the blackboard (the moat)
- Blackboard schema/versioning; the closed loop (feedback → Generator → oracle → …) runs entirely through shared state.
- Three payoffs: reproducible / ablatable / auditable. `[[FIG:blackboard]]`

### VII-C. Three agents
- Generator (source-search + invariant-query read-only tools; outputs seed + chosen checker set).
- Feedback agent (context-isolated subagent; turns coverage delta + Pass/NotApplicable into next-round guidance).
- Minimizer (deterministic creduce-led delta debugging; LLM only for semantic guidance).

### VII-D. Checker-routed seeds, ISA-bound checkers
- Agent answers only "which checkers can this seed trigger?"—it never touches the raw ISA axis.
- Cheap static checkers always-on; differential checkers force full ISA fan-out; superset (never-miss) selection with static-checker backstop. `[[FIG:checker-routing]]`

## VIII. Implementation

*Maps to: overview.md §2; agentic-loop-redesign.md §7.*

- Go core: `internal/service/{grpc_server,mcp_server}.go`, `internal/oracle` (checkers + metadata SSOT), toolchains.
- Python orchestrator: `graph.py` (fixed edge order + conditional routing), `state.py` (blackboard), `audit.py` (per-run dir + checkpointer), `routing.py`, three agents, deterministic nodes.
- LLM provider path; per-run audit artifacts (`checkpoints.sqlite`, `manifest.json`).

## IX. Evaluation

*Maps to: agentic-loop-redesign.md §8 (experimental hypotheses). Mirrors PropertyGPT §VIII (RQ1–4) and AgentFuzz §7.*

- **Setup.** Targets (instrumented GCC 16.1, cross-gcc, LLVM); ISAs (x86-64, AArch64, RISC-V, LoongArch); toolchains and QEMU.
- **RQ1 — Real bugs.** Silent-failure defects found on GCC/LLVM; upstream confirmation / CVE status. `[TBD]`
- **RQ2 — Invariant generation quality.** Cross-mechanism recall; BM25 vs embedding contribution; novelty. (11 union invariants; 94% lexical-collision filtered by analogy.)
- **RQ3 — Ablation.** Coverage feedback on/off; oracle grounding on/off; each inter-agent edge on/off; checker routing vs Cartesian full-fan-out. `[[FIG:ablation]]`
- **RQ4 — Explicit orchestration.** Trajectory reproducibility under a fixed blackboard version; stability/hit-rate vs free orchestration.
- **RQ5 — Agentic loop vs fuzz loop.** Gain on "reaching silent-failure bugs" over the prior coverage-driven fuzz loop.

## X. Discussion and Limitations

- Falsification-only scope (no exploit construction); GCC-16.1-pinned corpus; dense-retrieval non-determinism; checker-routing as optimization, not correctness.

## XI. Related Work

*Mirrors PropertyGPT §IX / AgentFuzz §3.*

- Compiler bug finding (Csmith, YARPGen, EMI) and why differential/crash oracles miss silent failures.
- Silent/security bugs in compilers (taxonomy studies, e.g., "Silent Bugs Matter", USENIX'23).
- LLM/RAG property & invariant generation (PropertyGPT NDSS'25; SpecAuditor S&P'26 — contrast: we reject entity-driven retrieval).
- LLM-agent fuzzing (AgentFuzz/AgentDoS USENIX'25/'26) — contrast: free-running vs explicitly-orchestrated.

## XII. Conclusion

- Restate: invariant-grounded oracle turns "agents find bugs" from a demo into a reproducible method for silent defense failures across the mechanism × ISA matrix.

---

### Appendix (planned)
- A. Full invariant taxonomy table.
- B. Cross-mechanism generated invariants XINV-001..011 (statement + observation + falsifiability).
- C. Checker catalog with ISA bindings.
- D. Per-run manifest / reproduction recipe.

### Figure register (placeholders)

| stem | section | subject |
| --- | --- | --- |
| `defense-role` | §I | defenses as the runtime last line |
| `stack-layout` | §I / §II-B | CVE-2023-4039 buggy stack frame |
| `defense-matrix` | §I / §II-A | defense × ISA two-dimensional space |
| `architecture` | §III | DeFuzz two-part architecture |
| `main-loop` | §III | single-iteration data flow |
| `invariant-taxonomy` | §IV-C | bottom-up root-cause families |
| `specgen-pipeline` | §V-B | distill→analogy→specialize→entailment |
| `bm25-vs-embedding` | §V-C | retrieval union/dedup |
| `oracle-flow` | §VI-A | invariant → verdict decision flow |
| `blackboard` | §VII-B | shared-state linkage / moat |
| `checker-routing` | §VII-D | checker-bound ISA fan-out |
| `ablation` | §IX | ablation results |
