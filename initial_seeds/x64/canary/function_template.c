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

// INV-SP-R03 ground truth on x86_64 glibc.
//
// Unlike AArch64 / RISC-V / LoongArch (where __stack_chk_guard is a
// regular global initialized in glibc's csu/libc-start.c), x86_64 glibc
// stores the canary value in TLS at fs:0x28 — there is no exported
// `__stack_chk_guard` symbol. Reading it the same way GCC does in the
// canary check (mov %fs:0x28, %reg) is therefore the only portable way
// to obtain the ground truth here. Verified against glibc x86_64 emit:
// see `cmp %fs:0x28, %rax` in the seed() epilogue of any
// -fstack-protector-strong build.
static inline uintptr_t read_canary_guard(void) {
  uintptr_t g;
  __asm__ volatile ("movq %%fs:0x28, %0" : "=r"(g));
  return g;
}

// Force seed() to keep an independent epilogue regardless of what the
// LLM annotated. Without `noinline` the seed body could be merged into
// run_scrub_probe and the post-call register snapshot would observe the
// inliner's output rather than seed's leaked residue. `used` blocks LTO
// from dropping the function. The redeclaration below the placeholder
// merges these attributes onto the LLM-provided definition.
void seed(int buf_size, int fill_size) __attribute__((noinline, used));

// INV-SP-R03 scrub probe: x86_64 System V AMD64 caller-saved GPRs.
// Caller-saved set per the AMD64 ABI: rax, rcx, rdx, rsi, rdi, r8-r11.
// We pin the snapshot pointer to a callee-saved register (r12) so it
// survives `call seed`, and rely on the input constraints "D" / "S" to
// place buf_size / fill_size into rdi / rsi at the asm boundary.
#define SCRUB_N_X64 9
NO_CANARY static void run_scrub_probe(int buf_size, int fill_size) {
  static const char *const SCRUB_NAMES[SCRUB_N_X64] = {
    "rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11",
  };
  uintptr_t snap[SCRUB_N_X64] = {0};
  register uintptr_t *snap_ptr __asm__("r12") = snap;

  __asm__ volatile (
    "call seed\n\t"
    "movq %%rax,   0(%%r12)\n\t"
    "movq %%rcx,   8(%%r12)\n\t"
    "movq %%rdx,  16(%%r12)\n\t"
    "movq %%rsi,  24(%%r12)\n\t"
    "movq %%rdi,  32(%%r12)\n\t"
    "movq %%r8,   40(%%r12)\n\t"
    "movq %%r9,   48(%%r12)\n\t"
    "movq %%r10,  56(%%r12)\n\t"
    "movq %%r11,  64(%%r12)\n\t"
    :
    : "D"(buf_size), "S"(fill_size), "r"(snap_ptr)
    : "rax", "rcx", "rdx", "r8", "r9", "r10", "r11", "memory", "cc"
  );

  uintptr_t guard = read_canary_guard();
  for (int i = 0; i < SCRUB_N_X64; i++) {
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
