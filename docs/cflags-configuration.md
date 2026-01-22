# CFlags Configuration Guide

## Overview

Compiler flags (CFlags) are now configurable in the compiler configuration files instead of being hardcoded. This provides better flexibility for different ISA architectures and compilation scenarios.

## GCC Build Optimization

The instrumented GCC compilers in `target_compilers/gcc-v12.2.0-x64/` and `target_compilers/gcc-v12.2.0-aarch64-cross-compile/` are built with **-O0** optimization level to:

1. **Ensure accurate coverage measurement** - Optimizations can merge or eliminate code paths, leading to inaccurate coverage data
2. **Enable easier debugging** - No inlining or code reordering
3. **Preserve CFG structure** - The control flow graph matches the source code more closely

The build scripts (`build-gcc-instrumented.sh`) explicitly set:
```bash
export CFLAGS="-g -O0"
export CXXFLAGS="-g -O0"
```

## Configuration Structure

In your compiler configuration file (e.g., `gcc-v12.2.0-x64-canary.yaml`), add the `cflags` section under `compiler`:

```yaml
compiler:
  path: "/path/to/gcc"
  
  # Compiler flags passed to GCC during compilation
  cflags:
    - "-fstack-protector-strong"  # Enable stack canary protection
    - "-O0"                         # No optimization for debugging
    - "--sysroot=/path/to/sysroot"  # For cross-compilation
    - "-B/path/to/gcc/lib"          # Additional library search path
    - "-L/path/to/libs"             # Linker library path
```

## Examples

### x64 Native Compilation

```yaml
compiler:
  path: "/path/to/gcc/xgcc"
  
  cflags:
    - "-fstack-protector-strong"
    - "-O0"
```

### AArch64 Cross-Compilation

```yaml
compiler:
  path: "/path/to/aarch64-linux-gnu-gcc"
  
  cflags:
    - "-fstack-protector-strong"
    - "-O0"
    - "--sysroot=/path/to/aarch64-sysroot/libc"
    - "-B/path/to/install/lib/gcc/aarch64-none-linux-gnu/12.2.1"
    - "-L/path/to/install/aarch64-none-linux-gnu/lib64"
  
  fuzz:
    use_qemu: true
    qemu_path: "qemu-aarch64"
    qemu_sysroot: "/path/to/aarch64-sysroot/libc"
```

## Important Notes

### -B Parameter Usage

The `-B` parameter can be specified in two places:

1. **PrefixPath** (in code): Used to find compiler components (cc1, as, ld)
   - Automatically set to the directory containing the GCC binary
   - Example: `/path/to/gcc/bin/`

2. **CFlags** (in config): Used to find additional libraries (crtbegin.o, libgcc)
   - Explicitly configured in the `cflags` section
   - Example: `-B/path/to/lib/gcc/aarch64-none-linux-gnu/12.2.1`

GCC supports multiple `-B` parameters and will search them in order. This design allows:
- The code to automatically handle compiler component lookup
- The configuration to specify architecture-specific library paths

### Default Behavior

If `cflags` is not specified in the configuration file, the fuzzer will use these defaults:
```
-fstack-protector-strong -O0
```

A warning will be logged when defaults are used.

## Migration Guide

If you have an existing configuration without the `cflags` section:

1. Add the `cflags` section to your compiler configuration file
2. Move any hardcoded flags from the code to the configuration
3. For cross-compilation, include sysroot and library paths in `cflags`

Before:
```yaml
compiler:
  path: "/path/to/gcc"
  # No cflags section
```

After:
```yaml
compiler:
  path: "/path/to/gcc"
  
  cflags:
    - "-fstack-protector-strong"
    - "-O0"
    # Add any additional flags needed
```

## Testing

After configuration changes, verify the flags are being used:

```bash
# Build and run a single iteration
./defuzz fuzz --max-iterations 1

# Check the compiled binary's flags
readelf -p .comment fuzz_out/x64/canary/build/seed_1
```

## Benefits

1. **Flexibility**: Different flags for different architectures/strategies
2. **Maintainability**: No need to modify code for new compilation scenarios
3. **Clarity**: All compilation settings in one place
4. **Version Control**: Easy to track changes to compilation flags

## LLM-Controlled CFlags (Dynamic)

In addition to configuration-based CFlags, LLM can specify additional compiler flags per seed to reach different compiler code paths.

### How It Works

1. LLM generates code and optionally adds a CFLAGS section:
```c
void seed(int buf_size, int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
}
// ||||| CFLAGS_START |||||
-fstack-protector-all
// ||||| CFLAGS_END |||||
```

2. The parser extracts these flags into `Seed.CFlags`
3. During compilation, flags are applied in order:
   - Config CFlags (from yaml) come first
   - Seed CFlags (from LLM) come last
   - GCC uses last occurrence for conflicting flags

### Use Case

Some compiler code paths are controlled by **compiler flags**, not code patterns:

| Flag | Code Path |
|------|-----------|
| `-fstack-protector` | DEFAULT mode (flag=1) |
| `-fstack-protector-strong` | STRONG mode (flag=3) |
| `-fstack-protector-all` | ALL mode (flag=2) |
| `-fstack-protector-explicit` | EXPLICIT mode (flag=4) |

This allows LLM to dynamically explore different compiler code paths by specifying appropriate flags.

### Security Note

All flags from LLM are accepted without validation. This is safe because:
- Execution happens in QEMU (sandboxed)
- Only affects compiler behavior, not host system

