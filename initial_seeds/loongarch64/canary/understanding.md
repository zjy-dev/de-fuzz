# LoongArch64 Stack Canary Understanding

## Objectives

This fuzzing configuration targets the **Stack Canary (Stack Protection)** implementation of GCC 15.2.0 on the LoongArch64 architecture. The goal is to discover potential vulnerabilities similar to CVE-2023-4039 (AArch64 stack protection bypass).

## LoongArch64 Architecture Features

LoongArch is a 64-bit RISC instruction set architecture independently designed by Loongson, with the following features related to stack protection:

### Register Conventions
- `$ra` (r1): Return Address register
- `$sp` (r3): Stack Pointer
- `$fp` (r22): Frame Pointer
- `$tp` (r2): Thread Pointer, used to access the canary value in TLS

### Stack Frame Layout
```
High Addr → [Caller's Frame]
            [Return Address]     ← Target to protect
            [Saved $fp]
            [Stack Canary]       ← Protection mechanism
            [Local Variables]
            [Buffer]             ← Overflow start point
Low Addr  → [Stack Pointer $sp]
```

### Canary Access Pattern
LoongArch64 uses Thread Local Storage (TLS) to access the canary:
```asm
# Typical canary load sequence
ld.d    $t0, $tp, CANARY_OFFSET   # Load canary from TLS
st.d    $t0, $sp, LOCAL_OFFSET    # Store to stack frame
```

## Compiler Code Paths (cfgexpand.cc)

### Key Functions

1. **`expand_stack_vars`** - Core function for stack variable allocation
   - Determines the position of variables in the stack frame
   - Handles alignment requirements
   - VLA and alloca trigger different allocation paths

2. **`expand_one_stack_var_at`** - Allocation of a single variable
   - Sets the stack offset for the variable
   - Handles special alignment needs

3. **`defer_stack_allocation`** - Defer allocation decision
   - Some variables may be deferred to runtime allocation
   - VLAs typically go through this path

4. **`stack_protect_decl_p`** - Decides if protection is needed
   - Checks if the variable is an array or an aggregate type containing an array
   - Decides whether to allocate within the canary protected area

## Potential Vulnerability Patterns

### 1. VLA (Variable Length Array)
```c
void seed(int buf_size, int fill_size) {
    char vla[buf_size];  // Dynamic size array
    memset(vla, 'A', fill_size);
}
```
- VLAs are allocated at runtime, potentially bypassing compile-time stack protection analysis
- Stack layout might place the canary in the wrong location

### 2. alloca()
```c
void seed(int buf_size, int fill_size) {
    char *buf = alloca(buf_size);
    memset(buf, 'A', fill_size);
}
```
- alloca dynamically adjusts the stack pointer
- May lead to incorrect canary location calculations

### 3. Mixed Patterns
```c
void seed(int buf_size, int fill_size) {
    char fixed[32];
    char vla[buf_size];
    char *dyn = alloca(16);
    // Complex stack layout exposes edge cases
}
```

### 4. Struct Containing Array
```c
void seed(int buf_size, int fill_size) {
    struct { char data[buf_size]; int value; } s;
    memset(s.data, 'A', fill_size);
}
```

### 5. Multi-level Nesting
```c
void helper(char *buf, int fill_size) {
    memset(buf, 'A', fill_size);
}
void seed(int buf_size, int fill_size) {
    char vla[buf_size];
    helper(vla, fill_size);
}
```

### 6. Conditional Allocation
```c
void seed(int buf_size, int fill_size) {
    char *buf;
    if (buf_size > 64) {
        buf = alloca(buf_size);
    } else {
        char local[64];
        buf = local;
    }
    memset(buf, 'A', fill_size);
}
```

### 7. VLA in Loop
```c
void seed(int buf_size, int fill_size) {
    for (int i = 0; i < 3; i++) {
        char vla[buf_size + i];
        memset(vla, 'A', fill_size);
    }
}
```

### 8. Alignment Sensitive Patterns
```c
void seed(int buf_size, int fill_size) {
    __attribute__((aligned(256))) char aligned_buf[buf_size];
    memset(aligned_buf, 'A', fill_size);
}
```

### 9. Zero-Length Array Boundary
```c
void seed(int buf_size, int fill_size) {
    char vla[buf_size > 0 ? buf_size : 1];
    memset(vla, 'A', fill_size);
}
```

### 10. Multi-Array Interaction
```c
void seed(int buf_size, int fill_size) {
    char first[buf_size];
    char second[64];
    char third[buf_size / 2];
    memset(first, 'A', fill_size);
}
```

## Generation Guidance

To maximize coverage and discover potential vulnerabilities, the LLM should:

1. **Prioritize VLA and alloca patterns** - These are most likely to expose stack layout issues
2. **Try different array sizes** - Trigger different alignment and allocation paths
3. **Combine multiple allocation methods** - Test complex stack layouts
4. **Use attribute modifiers** - `aligned`, `packed`, etc. may affect layout
5. **Explore boundary conditions** - Zero size, huge values, negative conversions, etc.
6. **Nested function calls** - Test stack protection across functions

## LoongArch64 Specific Considerations

1. **16-byte Stack Alignment** - LoongArch64 requires the stack pointer to be 16-byte aligned
2. **LP64D ABI** - Uses 64-bit pointers and long types
3. **Callee-saved Registers** - Saved registers may affect stack layout
4. **TLS Access Overhead** - Canary is loaded from TLS, potentially having specific access patterns

## Expected Output

- Normal Exit (exit 0): No overflow or overflow did not touch critical areas
- SIGABRT (exit 134): Canary detected overflow, working correctly
- SIGSEGV (exit 139): Return address modified, **potential vulnerability**

**CRITICAL**: If a SIGSEGV occurs at a fill_size smaller than one that causes SIGABRT, it indicates a defect in the canary protection.
