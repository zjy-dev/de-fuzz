## AArch64 Canary-Specific Instructions

On AArch64, GCC places dynamic allocations (VLA, alloca) BELOW saved registers on the stack. This affects how stack protection works.

**Priority patterns to generate:**

1. **VLA (Variable-Length Array)**: `char buf[buf_size];` where buf_size is a parameter
   - Uses dynamic stack allocation
   - Example: `void seed(int buf_size, int fill_size) { char buf[buf_size]; memset(buf, 'A', fill_size); printf("SEED_RETURNED\n"); fflush(stdout); }`

2. **alloca()**: `char *buf = alloca(buf_size);`
   - Also uses dynamic stack allocation
   - Example: `void seed(int buf_size, int fill_size) { char *buf = alloca(buf_size); memset(buf, 'A', fill_size); printf("SEED_RETURNED\n"); fflush(stdout); }`

3. **Mixed patterns**: Combine VLA/alloca with fixed-size arrays in the same function
   - Tests different stack layouts
   - Example: `char fixed[64]; char vla[buf_size];` (remember to print `SEED_RETURNED` before return)

**Sentinel requirement (must follow):**
- Always print `SEED_RETURNED` **inside `seed()` and before returning**:
  `printf("SEED_RETURNED\n"); fflush(stdout);`

**Stack layout differences:**
- Fixed arrays: Stack guard may protect the return address
- Dynamic arrays (VLA/alloca): Different stack layout, may affect protection
- Overflow behavior depends on where buffers are placed relative to saved registers
