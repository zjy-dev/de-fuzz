# PPC64LE Stack Canary Understanding

## Objectives

This fuzzing configuration targets the **Stack Canary (Stack Protection)** implementation of GCC 15.2.0 on **PowerPC64 Little Endian (ppc64le)**.
The main goal is to stress the PowerPC backend paths where stack layout, saved LR/CR state, parameter save areas, and dynamic stack allocation interact with canary placement.

## Why PPC64LE Is Interesting

Compared with x86/x86_64, the ppc64le backend has several properties that make stack-protector bugs more plausible:

1. **ELFv2 stack frames are structurally rich**
   - back chain
   - saved CR
   - saved LR
   - saved TOC pointer
   - parameter save area
   - alloca space
   - local variable space
   - optional ROP hash slot
   - GPR / FPR / AltiVec save areas

2. **Stack growth semantics change with SSP**
   In `gcc/gcc/config/rs6000/rs6000.h`, `FRAME_GROWS_DOWNWARD` depends on `flag_stack_protect != 0`.
   This means enabling stack protection changes how the backend reasons about the frame.

3. **Backend frame construction lives in `rs6000-logue.cc`**
   Functions like `rs6000_stack_info`, `rs6000_emit_prologue`, and `rs6000_emit_epilogue` are directly relevant to whether saved state is positioned safely relative to local buffers.

4. **Guard source is target-specific**
   In `rs6000.cc`, the stack protector can use:
   - global guard
   - TLS guard
   - a configurable TLS base register and offset

## PPC64LE Register / ABI Notes

### Important registers
- `r1`: stack pointer
- `r2`: TOC pointer (ELFv2 ABI)
- `r13`: TLS pointer / default stack protector base register on 64-bit Linux
- `lr`: link register (return address source)
- `cr`: condition register

### ABI baseline
- target: Linux `powerpc64le`
- default 64-bit ABI: **ELFv2**
- default CPU baseline should be at least **POWER8**
- dynamic linker: `/lib64/ld64.so.2`

## Relevant GCC Backend Paths

### `rs6000.cc`

1. **`rs6000_init_stack_protect_guard`**
   - implements the target hook for selecting the guard source

2. **`rs6000_stack_protect_fail`**
   - selects hidden vs external fail symbol variants
   - interacts with ABI / PIC details

> Note:
> `rs6000_option_override_internal` does validate many PowerPC options, but it
> also contains a very large tune / cost-model switch driven mostly by
> `-mcpu/-mtune`. That surface is intentionally **not** an active online canary
> target in the current ppc64le configuration because it tends to dominate
> target selection without being canary-specific enough.

### `rs6000-logue.cc`

1. **`rs6000_stack_info`**
   - computes frame sizes and offsets
   - decides where LR / CR / locals / save areas live
   - reacts to `calls_alloca`, ABI mode, vector saves, etc.

2. **`rs6000_emit_prologue_components` / `rs6000_emit_epilogue_components`**
   - lower frame operations into backend RTL pieces

3. **`rs6000_emit_prologue` / `rs6000_emit_epilogue`**
   - implement the final stack setup / teardown logic

### Generic stack-protector core

The usual generic functions are still important:
- `stack_protect_classify_type`
- `stack_protect_decl_phase*`
- `create_stack_guard`
- `expand_used_vars`
- `stack_protect_prologue`
- `stack_protect_epilogue`

## Vulnerability-Oriented Source Patterns

Prioritize patterns that disturb the relation between locals and saved state:

### 1. VLA
```c
void seed(int buf_size, int fill_size) {
    char vla[buf_size];
    memset(vla, 'A', fill_size);
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

### 2. alloca
```c
void seed(int buf_size, int fill_size) {
    char *buf = alloca(buf_size);
    memset(buf, 'A', fill_size);
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

### 3. Fixed + VLA + alloca mixture
```c
void seed(int buf_size, int fill_size) {
    char fixed[32];
    char vla[buf_size];
    char *tmp = alloca(24);
    memset(vla, 'A', fill_size);
    fixed[0] = tmp[0];
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

### 4. Alignment / vector-sensitive locals
```c
void seed(int buf_size, int fill_size) {
    __attribute__((aligned(16))) char a[buf_size];
    __attribute__((aligned(32))) char b[64];
    memset(a, 'A', fill_size);
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

### 5. Large frames with multiple arrays
```c
void seed(int buf_size, int fill_size) {
    char a[64];
    char b[128];
    char c[buf_size];
    memset(c, 'A', fill_size);
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

### 6. Explicit stack protection
```c
__attribute__((stack_protect))
void seed(int buf_size, int fill_size) {
    char vla[buf_size];
    memset(vla, 'A', fill_size);
    printf("SEED_RETURNED
");
    fflush(stdout);
}
```

## Compiler Flag Guidance

### Guard-source exploration
The ppc64le target canary matrix should explore:
- default target behavior
- `-mstack-protector-guard=global`
- `-mstack-protector-guard=tls`
- `-mstack-protector-guard-reg=r13`
- TLS offsets around the Linux default (`-28688`, i.e. `-0x7010`)

### SSP policy exploration
Useful policies:
- `-fstack-protector`
- `-fstack-protector-strong`
- `-fstack-protector-all`
- `-fstack-protector-explicit`

### `-fstack-protector-explicit`
If this profile is active, `seed()` must use `__attribute__((stack_protect))`, otherwise no canary will be inserted.

## Expected Oracle Outcomes

- exit `0`: normal execution
- exit `134`: canary fired (`__stack_chk_fail`) → protected
- exit `139`: control-flow corruption / invalid return path → candidate bug

A candidate is especially interesting if a smaller `fill_size` reaches `139` before a larger size reaches `134`.

## Practical Generation Advice

1. Prefer **VLA / alloca / mixed-frame** patterns first.
2. Keep the crashing primitive simple: usually `memset` is enough.
3. Preserve the sentinel output before return.
4. Avoid `__attribute__((no_stack_protector))` on `seed()` unless the profile is explicitly negative control.
5. When using explicit SSP mode, remember `__attribute__((stack_protect))`.
