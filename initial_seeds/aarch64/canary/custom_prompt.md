## AArch64 Canary-Specific Instructions

CRITICAL: CVE-2023-4039 - VLA and alloca() bypass stack canary on AArch64!

On AArch64, GCC places dynamic allocations (VLA, alloca) BELOW saved registers on the stack, bypassing the stack guard.

**Priority patterns to generate:**

1. **VLA (Variable-Length Array)**: `char buf[buf_size];` where buf_size is a parameter
   - This bypasses canary protection on AArch64
   - Example: `void seed(int buf_size, int fill_size) { char buf[buf_size]; memset(buf, 'A', fill_size); }`

2. **alloca()**: `char *buf = alloca(buf_size);`
   - Also bypasses canary protection on AArch64
   - Example: `void seed(int buf_size, int fill_size) { char *buf = alloca(buf_size); memset(buf, 'A', fill_size); }`

3. **Mixed patterns**: Combine VLA/alloca with fixed-size arrays in the same function
   - Tests different stack layouts
   - Example: `char fixed[64]; char vla[buf_size];`

**Why this matters:**
- On x64: VLA is protected by stack guard ✓
- On AArch64: VLA is NOT protected by stack guard ✗
- Overflow in VLA can directly overwrite return address without triggering canary check
