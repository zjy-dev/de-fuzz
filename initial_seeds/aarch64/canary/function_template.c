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

// INV-SP-R03 ground truth on AArch64. Default `-mstack-protector-guard=global`
// glibc exports `__stack_chk_guard` as a regular (non-TLS) global; we read
// it directly. Declared weak so binaries built with
// `-mstack-protector-guard=sysreg` (where the symbol does not exist) still
// link; the run_scrub_probe path detects the NULL address and emits
// CANARY_SCRUB_NA. See docs/invariants/stack-canary.md INV-SP-R03.
extern uintptr_t __stack_chk_guard __attribute__((weak));

// Force seed() to keep an independent epilogue regardless of what the
// LLM annotated. Without `noinline` the seed body could be merged into
// run_scrub_probe and the post-call register snapshot would observe the
// inliner's output rather than seed's leaked residue. `used` blocks LTO
// from dropping the function. The redeclaration here merges these
// attributes onto the LLM-provided definition above.
void seed(int buf_size, int fill_size) __attribute__((noinline, used));

// INV-SP-R03 scrub probe: AArch64 AAPCS64 caller-saved GPRs.
// Per AAPCS64, registers x0-x18 are caller-saved (x9-x15 temporaries,
// x16-x17 IPC, x18 platform). x30 is LR — caller-saved but its post-call
// value is the return address by definition, not a leaked guard, so we
// skip it. We pin the snapshot pointer to a callee-saved register (x19)
// so it survives `bl seed`, and pin the int args to w0/w1 via register
// variables so GCC emits the loads BEFORE the asm.
#define SCRUB_N_AARCH64 19
NO_CANARY static void run_scrub_probe(int buf_size, int fill_size) {
  static const char *const SCRUB_NAMES[SCRUB_N_AARCH64] = {
    "x0",  "x1",  "x2",  "x3",  "x4",  "x5",  "x6",  "x7",  "x8",  "x9",
    "x10", "x11", "x12", "x13", "x14", "x15", "x16", "x17", "x18",
  };
  uintptr_t snap[SCRUB_N_AARCH64] = {0};
  register uintptr_t *snap_ptr __asm__("x19") = snap;
  register int bs_in __asm__("w0") = buf_size;
  register int fs_in __asm__("w1") = fill_size;

  __asm__ volatile (
    "bl seed\n\t"
    "stp  x0,  x1,  [x19, #0]\n\t"
    "stp  x2,  x3,  [x19, #16]\n\t"
    "stp  x4,  x5,  [x19, #32]\n\t"
    "stp  x6,  x7,  [x19, #48]\n\t"
    "stp  x8,  x9,  [x19, #64]\n\t"
    "stp  x10, x11, [x19, #80]\n\t"
    "stp  x12, x13, [x19, #96]\n\t"
    "stp  x14, x15, [x19, #112]\n\t"
    "stp  x16, x17, [x19, #128]\n\t"
    "str  x18,      [x19, #144]\n\t"
    : "+r"(snap_ptr)
    : "r"(bs_in), "r"(fs_in)
    : "x2", "x3", "x4", "x5", "x6", "x7", "x8", "x9", "x10", "x11",
      "x12", "x13", "x14", "x15", "x16", "x17", "x18", "x30",
      "memory", "cc"
  );

  // Weak-undefined fallback: __stack_chk_guard symbol absent (sysreg mode).
  if (&__stack_chk_guard == (void *)0) {
    printf("CANARY_SCRUB_NA reason=no_guard_symbol\n");
    fflush(stdout);
    _exit(0);
  }
  uintptr_t guard = __stack_chk_guard;
  for (int i = 0; i < SCRUB_N_AARCH64; i++) {
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
