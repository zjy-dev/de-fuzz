# AArch64 Stack Canary Fuzzing

## Goal
Generate C code to test GCC's stack canary (stack protector) implementation on AArch64. Your code will be compiled with `-fstack-protector-strong` and analyzed for coverage of canary-related compiler functions.

## Target Functions (cfgexpand.cc)
These are the GCC functions we want to trigger different code paths in:

- `stack_protect_classify_type` - Classifies variable types to determine protection needs (char arrays, aggregates, etc.)
- `stack_protect_decl_phase` - Decides which protection phase (1 or 2) a variable belongs to
- `stack_protect_decl_phase_1` - First phase variable handling
- `stack_protect_decl_phase_2` - Second phase variable handling  
- `add_stack_protection_conflicts` - Adds protection conflict constraints between stack variables
- `create_stack_guard` - Creates the guard variable on stack
- `stack_protect_prologue` - Generates function prologue code that sets up the canary
- `stack_protect_return_slot_p` - Checks for return slot usage (used by -fstack-protector-strong)

## Attack Surface - Code Patterns to Generate

### Buffer Types
- `char buf[N]` - Character arrays (most common, triggers char array detection)
- `int arr[N]` - Integer arrays
- `struct { char data[N]; }` - Structs containing arrays
- `union` with array members
- Arrays of structs

### Buffer Sizes to Vary
- Small: 8, 16, 32 bytes
- Medium: 64, 128 bytes  
- Large: 256, 512, 1024 bytes

### Overflow Patterns
- `memset(buf, 'A', fill_size)` - Basic overflow
- `memcpy(buf, src, fill_size)` - Copy-based overflow
- Loop with pointer increment: `for(i=0; i<fill_size; i++) *p++ = 'A';`
- `strcpy` / `strncpy` patterns
- Off-by-one writes

### Stack Layout Variations
- Single large buffer
- Multiple buffers of different sizes
- Buffer + local variables (int, pointer)
- Buffer + function pointer on stack
- Buffer + saved register spill area

### Function Call Patterns
- Direct overflow in leaf function
- Nested function calls with buffers at each level
- Recursive functions with local buffers
- Indirect calls through function pointers

### Variable declarations
- Stack arrays with const size
- VLA (variable length arrays): `char buf[n];`
- `alloca()` usage

## Output Rules
- Generate ONLY the `seed(int fill_size)` function body
- Use local buffers and overflow them based on `fill_size` parameter
- NO explanations, NO markdown formatting
- Output raw C code only