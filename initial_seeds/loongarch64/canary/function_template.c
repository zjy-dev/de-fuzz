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
 * Function Attributes:
 * - __attribute__((stack_protect)) - Force stack protection (use with -fstack-protector-explicit)
 * - __attribute__((no_stack_protector)) - Disable stack protection for this function
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

// INV-SP-R03 ground truth on LoongArch64. LoongArch GCC's generic stack
// protector emits a load of the global `__stack_chk_guard`. Declared
// weak so binaries without the symbol still link; the run_scrub_probe
// path detects the NULL address and emits CANARY_SCRUB_NA.
//
// LoongArch64 is the primary target of DREV-2026-001: gcc-16.1.0-RC
// loongarch64-linux-gnu cross emits the canary check via a generic
// fallback that does NOT scrub $r12 / $r13 between the comparison and
// `jr $r1`, so the guard value persists in caller-saved $r12 across the
// return. This template is the runtime detector for that bug.
extern uintptr_t __stack_chk_guard __attribute__((weak));

// Force seed() to keep an independent epilogue regardless of what the
// LLM annotated. Without `noinline` the seed body could be merged into
// run_scrub_probe and the post-call register snapshot would observe the
// inliner's output rather than seed's leaked residue. `used` blocks LTO
// from dropping the function. The redeclaration here merges these
// attributes onto the LLM-provided definition above.
void seed(int buf_size, int fill_size) __attribute__((noinline, used));

// INV-SP-R03 scrub probe: LoongArch64 LP64 caller-saved GPRs.
// Per the LoongArch psABI the caller-saved set is:
//   ra ($r1), a0-a7 ($r4-$r11), t0-t8 ($r12-$r20)        = 18 registers.
// ($r21 is reserved; $r22 is fp; $r23-$r31 are s0-s8 callee-saved.)
// We pin the snapshot pointer to $r23 (s0) so it survives `bl seed`,
// and pin the int args to $r4 (a0) / $r5 (a1) via register variables.
#define SCRUB_N_LOONGARCH64 18
NO_CANARY static void run_scrub_probe(int buf_size, int fill_size) {
  static const char *const SCRUB_NAMES[SCRUB_N_LOONGARCH64] = {
    "ra",
    "a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7",
    "t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8",
  };
  uintptr_t snap[SCRUB_N_LOONGARCH64] = {0};
  register uintptr_t *snap_ptr __asm__("$r23") = snap;
  register int bs_in __asm__("$r4") = buf_size;
  register int fs_in __asm__("$r5") = fill_size;

  __asm__ volatile (
    "bl seed\n\t"
    "st.d $r1,  $r23,   0\n\t"   /* ra */
    "st.d $r4,  $r23,   8\n\t"   /* a0 */
    "st.d $r5,  $r23,  16\n\t"   /* a1 */
    "st.d $r6,  $r23,  24\n\t"   /* a2 */
    "st.d $r7,  $r23,  32\n\t"   /* a3 */
    "st.d $r8,  $r23,  40\n\t"   /* a4 */
    "st.d $r9,  $r23,  48\n\t"   /* a5 */
    "st.d $r10, $r23,  56\n\t"   /* a6 */
    "st.d $r11, $r23,  64\n\t"   /* a7 */
    "st.d $r12, $r23,  72\n\t"   /* t0 */
    "st.d $r13, $r23,  80\n\t"   /* t1 */
    "st.d $r14, $r23,  88\n\t"   /* t2 */
    "st.d $r15, $r23,  96\n\t"   /* t3 */
    "st.d $r16, $r23, 104\n\t"   /* t4 */
    "st.d $r17, $r23, 112\n\t"   /* t5 */
    "st.d $r18, $r23, 120\n\t"   /* t6 */
    "st.d $r19, $r23, 128\n\t"   /* t7 */
    "st.d $r20, $r23, 136\n\t"   /* t8 */
    : "+r"(snap_ptr)
    : "r"(bs_in), "r"(fs_in)
    : "$r1", "$r6", "$r7", "$r8", "$r9", "$r10", "$r11",
      "$r12", "$r13", "$r14", "$r15", "$r16", "$r17", "$r18", "$r19", "$r20",
      "memory"
  );

  if (&__stack_chk_guard == (void *)0) {
    printf("CANARY_SCRUB_NA reason=no_guard_symbol\n");
    fflush(stdout);
    _exit(0);
  }
  uintptr_t guard = __stack_chk_guard;
  for (int i = 0; i < SCRUB_N_LOONGARCH64; i++) {
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
