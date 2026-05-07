/**
 * Canary Oracle Function Template - Flexible Version
 *
 * This template is used for testing stack canary protection mechanisms.
 * The LLM generates only the seed() function body.
 *
 * Usage: ./prog <buf_size> <fill_size>
 *   - buf_size:  Size of buffer to allocate (used for VLA/alloca, ignored for fixed)
 *   - fill_size: Number of 'A' characters to write into the buffer
 *   - Example: ./prog 64 128 (allocate 64-byte buffer, write 128 bytes)
 *
 * Expected behavior:
 *   - Small fill_size: Program exits normally (return 0)
 *   - Medium fill_size (canary overwritten): SIGABRT (exit code 134)
 *   - Large fill_size (ret addr overwritten): SIGSEGV (exit code 139)
 *
 * IMPORTANT FOR AARCH64:
 *   VLA and alloca() have different stack layouts than fixed-size arrays.
 *
 * The canary oracle uses binary search on fill_size to detect vulnerabilities.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <alloca.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement a function with signature: void seed(int buf_size, int fill_size)
 * - The function should contain a local buffer that can be overflowed
 * - buf_size: controls buffer allocation size (for VLA/alloca)
 * - fill_size: controls how many bytes to write
 * - You CAN add function attributes like __attribute__((stack_protect)) if needed
 *
 * CRITICAL: SENTINEL REQUIREMENT
 * - You MUST add the following two lines BEFORE the function returns:
 *     printf("SEED_RETURNED\n");
 *     fflush(stdout);
 * - This sentinel is used by the oracle to distinguish true canary bypass
 *   (crash on return) from false positives (crash inside function).
 * - If sentinel is missing, the oracle cannot properly detect bugs!
 *
 * Supported patterns:
 * 1. Fixed-size array (ignores buf_size):
 *    void seed(int buf_size, int fill_size) {
 *        char buffer[64];
 *        memset(buffer, 'A', fill_size);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 *
 * 2. Variable-Length Array (VLA):
 *    void seed(int buf_size, int fill_size) {
 *        char buffer[buf_size];
 *        memset(buffer, 'A', fill_size);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 *
 * 3. alloca():
 *    void seed(int buf_size, int fill_size) {
 *        char *buffer = alloca(buf_size);
 *        memset(buffer, 'A', fill_size);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 *
 * 4. Mixed patterns:
 *    void seed(int buf_size, int fill_size) {
 *        char fixed[32];
 *        char vla[buf_size];
 *        // test different combinations
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 */

// Disable stack protector for main / scrub probe so the seed callee is
// the only function that can possibly invoke __stack_chk_fail. This
// realizes INV-SP-A01 ("main has no canary slot") at the binary level.
#define NO_CANARY __attribute__((no_stack_protector))

// INV-SP-R03 ground truth on RISC-V LP64. RISC-V GCC's generic stack
// protector emits a load of the global `__stack_chk_guard`. Declared
// weak so binaries without the symbol still link; the run_scrub_probe
// path detects the NULL address and emits CANARY_SCRUB_NA.
// See docs/invariants/stack-canary.md INV-SP-R03 — RISC-V is one of the
// generic-fallback backends expected to produce GUARD_LEAKED.
extern uintptr_t __stack_chk_guard __attribute__((weak));

// Force seed() to keep an independent epilogue regardless of what the
// LLM annotated. Without `noinline` the seed body could be merged into
// run_scrub_probe and the post-call register snapshot would observe the
// inliner's output rather than seed's leaked residue. `used` blocks LTO
// from dropping the function. The redeclaration here merges these
// attributes onto the LLM-provided definition above.
void seed(int buf_size, int fill_size) __attribute__((noinline, used));

// INV-SP-R03 scrub probe: RISC-V LP64 caller-saved GPRs.
// Per the RISC-V calling convention, the caller-saved set is:
//   ra (x1), t0-t6 (x5-x7, x28-x31), a0-a7 (x10-x17)  = 16 registers.
// (gp/x3 is the global pointer; tp/x4 is the thread pointer; both must
// not be touched. s0-s11 are callee-saved.) ra holds the return address
// after `call seed`, but on this path that's the address of our snapshot
// store — not a leaked guard — so a coincidental match is impossible in
// practice. We pin the snapshot pointer to s2 (a callee-saved register
// that doesn't conflict with the frame pointer s0 = x8).
#define SCRUB_N_RISCV64 16
NO_CANARY static void run_scrub_probe(int buf_size, int fill_size) {
  static const char *const SCRUB_NAMES[SCRUB_N_RISCV64] = {
    "ra",
    "t0", "t1", "t2", "t3", "t4", "t5", "t6",
    "a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7",
  };
  uintptr_t snap[SCRUB_N_RISCV64] = {0};
  register uintptr_t *snap_ptr __asm__("s2") = snap;
  register int bs_in __asm__("a0") = buf_size;
  register int fs_in __asm__("a1") = fill_size;

  __asm__ volatile (
    "call seed\n\t"
    "sd ra,   0(s2)\n\t"
    "sd t0,   8(s2)\n\t"
    "sd t1,  16(s2)\n\t"
    "sd t2,  24(s2)\n\t"
    "sd t3,  32(s2)\n\t"
    "sd t4,  40(s2)\n\t"
    "sd t5,  48(s2)\n\t"
    "sd t6,  56(s2)\n\t"
    "sd a0,  64(s2)\n\t"
    "sd a1,  72(s2)\n\t"
    "sd a2,  80(s2)\n\t"
    "sd a3,  88(s2)\n\t"
    "sd a4,  96(s2)\n\t"
    "sd a5, 104(s2)\n\t"
    "sd a6, 112(s2)\n\t"
    "sd a7, 120(s2)\n\t"
    : "+r"(snap_ptr)
    : "r"(bs_in), "r"(fs_in)
    : "ra", "t0", "t1", "t2", "t3", "t4", "t5", "t6",
      "a2", "a3", "a4", "a5", "a6", "a7",
      "memory"
  );

  if (&__stack_chk_guard == (void *)0) {
    printf("CANARY_SCRUB_NA reason=no_guard_symbol\n");
    fflush(stdout);
    _exit(0);
  }
  uintptr_t guard = __stack_chk_guard;
  for (int i = 0; i < SCRUB_N_RISCV64; i++) {
    if (snap[i] == guard) {
      printf("GUARD_LEAKED reg=%d name=%s\n", i, SCRUB_NAMES[i]);
      fflush(stdout);
      _exit(1);
    }
  }
  printf("CANARY_SCRUB_OK\n");
  fflush(stdout);
  _exit(0);
}

NO_CANARY int main(int argc, char *argv[]) {
  // INV-SP-R03 scrub mode: argv = "scrub". Dispatched first so a
  // misuse of the binary-search path cannot accidentally trigger it.
  if (argc == 2 && strcmp(argv[1], "scrub") == 0) {
    run_scrub_probe(64, 0);
    return 1;  // unreachable; run_scrub_probe always _exit()s.
  }

  // INV-SP-L01 binary-search mode (existing path, unchanged).
  if (argc != 3) {
    fprintf(stderr,
            "Usage: %s <buf_size> <fill_size>\n"
            "   or: %s scrub\n",
            argv[0], argv[0]);
    return 1;
  }

  int buf_size = atoi(argv[1]);
  int fill_size = atoi(argv[2]);
  
  if (buf_size < 0 || fill_size < 0) {
    fprintf(stderr, "Error: sizes must be non-negative\n");
    return 1;
  }

  // Call the seed function with both parameters
  seed(buf_size, fill_size);

  return 0;
}
