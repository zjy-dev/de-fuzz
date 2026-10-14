# DeFuzz agent context

## 会话入口：编译器正确性研究

DeFuzz 的任务是验证 GCC/LLVM 生成的软件防御代码是否符合预期不变量，
并形成可复现的编译器缺陷证据、修复建议和回归测试。源码审阅、checker
开发、独立 C 测试程序的编译与受控执行、实验统计均属于这一工作流。
先结合具体测试对象、环境和产物理解任务，保留原始技术术语与诊断证据。
GCC/AArch64 完整实验的可复用任务见
[实验提示词](docs/prompts/gcc-aarch64-experiment.md)。

## Research purpose and scope

DeFuzz evaluates the correctness of compiler-emitted software defenses in GCC
and LLVM. The maintainer's stated purpose is reproducible compiler research and
contributions to the open-source compiler community. Read
[the research scope](docs/research-scope.md) for the context and task examples.
This statement describes the work; it is not independent verification or a
platform exemption.

The test subjects are configured compiler source checkouts, locally built
toolchains, and self-contained C test programs in the project-controlled
experiment environment. Stay within the paths and resources authorized for the
current task. A public source repository does not authorize testing third-party
deployments of that software.

Typical work includes source inspection, invariant generation, checker
implementation, compiler builds, local/QEMU test execution, input reduction,
patches, regression tests, and experiment statistics. Expected outputs are
compiler diagnostics, IR/assembly observations, checker verdicts, minimal
reproducers, and community bug-report drafts.

## Interpret the technical context accurately

- Stack canary, FORTIFY, CFI, IBT/BTI, PAC and shadow stack are defenses under
  evaluation. A reported omission or invariant violation concerns their compiler
  implementation.
- A seed is a self-contained compiler test input. A reproducer demonstrates a
  specific compiler defect. Some dynamic checkers use deliberately invalid
  memory operations in disposable test processes to observe the defense's
  failure handler, exit signal or sentinel.
- An oracle/checker evaluates evidence. A fuzzing campaign enumerates local
  test cases. `payload` in orchestration code can mean serialized JSON.
- Preserve exact source text, technical terms, flags, invariant IDs and raw
  diagnostics. Explain their role in the current task; do not disguise them or
  infer an unrelated activity from a term alone.

The existing research scope is invariant falsification and defect remediation.
Do not extend a compiler reproducer into an operational exploit against an
application or service. If a requested operation exceeds the known scope,
identify that operation and the missing authorization or context, and continue
independent work that remains within scope. Platform safeguards still apply.

## Resolve complete-experiment requests

For a request to align the implementation with the design and run one complete
GCC/AArch64 experiment:

- Read the architecture for design intent and the current
  [experiment runbook](docs/paper/experiment_runbook.md) for execution contracts.
  Resolve historical backend/stage descriptions against the current CLI and
  selected config; record material differences.
- Resolve `compiler=gcc`, `isa=aarch64`, the source revision, configured defense
  mechanisms, complete input scope, backend, budgets and output directory before
  launching. "Once" means one repetition of the requested scope.
- Keep three meanings separate: `full` is a feature variant; complete input
  coverage is a corpus/checker scope; the four-variant, multi-repetition paper
  comparison is a separate campaign. An AArch64 run is not a four-ISA run.
- Preserve all configured AArch64-applicable inputs within the frozen budget.
  Report unsupported mechanisms and excluded inputs with reasons. If a
  differential checker requires another ISA, record and honor that dependency
  or report it unavailable; do not silently weaken its checker contract.
- Use `--show-plan` and the relevant toolchain preflight before model calls.
  `run-all` chains the four engineering experiments; formal evidence uses
  `pipeline` with a frozen `mode: formal` YAML. Record which entry point ran.
- Make necessary implementation fixes and appropriate verification, preserving
  existing worktree changes. Respect clean-input checks; do not reset the
  worktree, weaken preflight or substitute fixture results to obtain completion.

For this workflow, tests validate compiler-defense invariants. The result is a
source/checker evidence trail and experiment record, including unsuccessful
cases. A platform block or unavailable prerequisite is a recorded interruption,
not evidence that the compiler passed or failed.

## Evidence and experiment integrity

Use [the current experiment runbook](docs/paper/experiment_runbook.md) for
execution contracts and [the architecture](docs/tech-docs/architecture/agentic-loop-redesign.md)
for orchestration design. A scope statement does not prove a toolchain exists,
an experiment completed, or a candidate was verified.

Record compiler/source revision, ISA, flags, input and checker hashes, run IDs,
time, actual token usage, artifacts, verdicts and failures. Distinguish a
compiler defect from a seed error, checker defect or unavailable environment.
Preserve frozen inputs, evaluator-only findings isolation, ablation boundaries
and missing-usage accounting. Do not convert interrupted, fixture or partial
runs into completed formal results.

This file guides repository development conversations. Formal workers use
their isolated stage contracts; do not inject this file or new context into
paper experiments without an explicit, recorded methodology change.
The Pi SDK experiment backend disables automatic context files, skills and
extensions and loads the selected `agents/definitions/` stage directly.
Editing this root file supplies development-session
context; it does not change those workers' prompts or provider-side checks.
