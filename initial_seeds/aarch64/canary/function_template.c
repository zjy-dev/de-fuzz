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
 *   VLA and alloca() bypass stack canary on AArch64! (CVE-2023-4039)
 *
 * The canary oracle uses binary search on fill_size to detect vulnerabilities.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement a function with signature: void seed(int buf_size, int fill_size)
 * - The function should contain a local buffer that can be overflowed
 * - buf_size: controls buffer allocation size (for VLA/alloca)
 * - fill_size: controls how many bytes to write
 * - DO NOT add any stack protection attributes to this function
 *
 * Supported patterns:
 * 1. Fixed-size array (ignores buf_size):
 *    void seed(int buf_size, int fill_size) {
 *        char buffer[64];
 *        memset(buffer, 'A', fill_size);
 *    }
 *
 * 2. Variable-Length Array (VLA - bypasses canary on AArch64):
 *    void seed(int buf_size, int fill_size) {
 *        char buffer[buf_size];
 *        memset(buffer, 'A', fill_size);
 *    }
 *
 * 3. alloca() (also bypasses canary on AArch64):
 *    void seed(int buf_size, int fill_size) {
 *        char *buffer = alloca(buf_size);
 *        memset(buffer, 'A', fill_size);
 *    }
 *
 * 4. Mixed patterns:
 *    void seed(int buf_size, int fill_size) {
 *  char fixed[32];
 *        char vla[buf_size];
 *        // test different combinations
 *    }
 */

// Disable stack protector for main to maximize attack surface
// This ensures canary check only happens in seed() if at all
#define NO_CANARY __attribute__((no_stack_protector))

NO_CANARY int main(int argc, char *argv[]) {
  if (argc != 3) {
    fprintf(stderr, "Usage: %s <buf_size> <fill_size>\n", argv[0]);
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
