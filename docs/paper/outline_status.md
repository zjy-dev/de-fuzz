# DeFuzz - IEEE S&P Paper Outline Status

This document tracks the completion status of the DeFuzz paper outline, ensuring alignment with the IEEE S&P (Oakland) target structure.

**Legend:**
- [x] **Completed:** Drafted, academically refined, and technically grounded.
- [ ] **Pending/Placeholder:** Requires experimental data or further writing.

---

## 0. Abstract
- [x] Abstract
  - [x] Context: Compiler defenses as the last line of memory safety.
  - [x] Problem: Silent failures and the oracle gap across the mechanism × ISA matrix.
  - [x] Solution: Machine-checkable oracle, cross-mechanism RAG pipeline, explicitly orchestrated agentic loop.

## 1. Introduction
- [x] 1.1 Silent failure
- [x] 1.2 Two coupled challenges
- [x] 1.3 Our approach
- [x] 1.4 Contributions
  - *Added strong hooks, formalized the 'double-silent' definition, and inserted [DEFERRED: N] placeholders for final quantitative claims.*

## 2. Background and Motivation
- [x] 2.1 Compiler Defenses in the Backend Lowering Pipeline
  - *Added deep compiler backend theory (IR vs. RTL/SelectionDAG).*
- [x] 2.2 Silent Failure: A Representative Case
- [x] 2.3 Why Existing Methods Miss It
- [x] 2.4 Threat Model and Scope
  - *Added formal Assumptions, In-scope, and Out-of-scope boundaries.*

## 3. System Overview
- [x] System Overview
  - *Decoupled offline knowledge construction vs. online compiler evaluation.*

## 4. Security Invariants for Compiler Defenses
- [x] 4.1 What is a security invariant
- [x] 4.2 Corpus Construction and Candidate Extraction
- [x] 4.3 Machine-Checkable Form: Static vs. Dynamic

## 5. Cross-Mechanism Invariant Generation
- [x] 5.1 Motivation: abstract-failure-mode transfer
- [x] 5.2 Pipeline: distill → analogy → specialize → entailment
  - *Includes CVE-2023-4039 running example.*
- [x] 5.3 Retrieval: BM25 and embedding are complementary
  - *Includes hybrid query design (`root_cause_phrase` & `critical_tokens`).*
- [x] 5.4 Grounding gates and reproducibility

## 6. Oracle: From Invariants to Verdicts
- [x] 6.1 Checker design
  - *Includes formal mathematical definition of the Oracle function.*
- [x] 6.2 Mechanism aggregator and flag profiles
- [x] 6.3 Checker metadata as a single source of truth

## 7. Agentic Loop
- [x] 7.1 Design principles
- [x] 7.2 Explicit orchestration and the blackboard
  - *Includes `Algorithm 1: Explicitly Orchestrated Agentic Loop`.*
- [x] 7.3 Three agents
- [x] 7.4 Checker-routed seeds, ISA-bound checkers

## 8. Implementation
- [x] Implementation
  - *Architectural description (Go core + Python orchestrator, gRPC/MCP) replacing code paths.*

## 9. Evaluation
- [ ] Setup
  - *DEFERRED: Needs LLM parameters, baseline configs, and exact computational budget.*
- [ ] RQ1 — Real bugs
  - *DEFERRED: Needs confirmed bug counts and CVE IDs.*
- [ ] RQ2 — Invariant generation quality
  - *DEFERRED: Needs expert grading and Cohen's Kappa.*
- [ ] RQ3 — Ablation
  - *DEFERRED: Needs numbers for explicit orchestration vs. free-running.*
- [ ] RQ4 — Cross-ISA generalization
  - *DEFERRED: Needs numbers.*

## 10. Discussion and Limitations
- [ ] Discussion and Limitations
  - *DEFERRED: Needs manual effort, LLM cost, and false positive analysis.*

## 11. Related Work
- [x] Compiler Bug Finding and The Oracle Gap
  - *Critiqued Csmith/YARPGen's structural blind spots.*
- [x] Silent and Security Bugs in Compilers
- [x] LLM and RAG-based Property Generation
  - *Contrasted entity-centric retrieval vs. our abstract-failure-mode transfer.*
- [x] LLM-Agent Fuzzing: Hallucination vs. Determinism
  - *Critiqued free-running agent hallucinations.*

## 12. Conclusion
- [x] Conclusion
  - *Rewritten to summarize the oracle gap, the deterministic orchestration solution, and the final impact placeholders.*
