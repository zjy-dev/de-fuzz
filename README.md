# DeFuzz

## Idea

> Seeds in DeFuzz have three types:

1. probably buggy c file + compile command that from c to binary
2. probably buggy c file + compile command that from c to asm(then literally compile it to asm) + llm fine-tuning asm + compile command that from asm to binary
3. probably buggy asm file + compile command that from asm to binary

---

> For each defense startegy and ISA:

1. prepare environment with podman and qemu
2. build initial prompt `ip`:
    - current environment and toolchain
    - manually summarize defense startegy and stack layout of the ISA
    - manually summarize pesudo-code of the compiler source code about that startegy and ISA
    - also reserve source code as an "attachment" below
3. feed `ip` to llm and store its "understanding" memory
4. initialize seed pool:
    - let llm generate initial seeds
    - adjust init seed pool mannualy
5. pop a seed `s` from seed pool
6. run s and record feedback `fb`(return code + stdout/stderr + logfile)
7. let llm analyze info of `s` + `fb` and act conditionally:
    <!-- TODO: May change to Multi-armed bandit later -->
    1. is a bug😊!!! -> record in detail -> `bug_cnt++` -> llm mutate `s` and push to seed pool
    2. not a bug -> let llm decide whether to discard `s` (is `s` meaningless?)
        - if not to discard, then mutate `s` and push to seed pool
8. if
    - `bug_cnt >= 3`, exit with success🤗!!!
    - `seed pool is empty`, exit with fail😢.
    - back to step 5😾.
