/**
 * Fortify Oracle Function Template
 *
 * This template is used for testing GCC/glibc FORTIFY behavior.
 * The LLM generates only the seed() function body.
 *
 * Usage: ./prog <buf_size> <fill_size>
 *   - buf_size: target buffer size used by the seed
 *   - fill_size: copy/write size used to trigger overflow or checked builtin paths
 *
 * Expected behavior:
 *   - Normal: exit 0
 *   - FORTIFY caught: SIGABRT / exit 134
 *   - FORTIFY bypassed: SIGSEGV / exit 139
 *
 * IMPORTANT:
 *   - Use libc APIs such as memcpy/strcpy/sprintf/fgets/read, not manual loops.
 *   - The oracle relies on stack protector being disabled by compiler flags.
 *   - You MUST print the sentinel before returning.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>
#include <unistd.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement: void seed(int buf_size, int fill_size)
 * - Use a dangerous libc API family relevant to FORTIFY:
 *   memcpy, memmove, strcpy, strncpy, strcat, sprintf, snprintf, fgets, read, getcwd, etc.
 * - Prefer source patterns that affect object-size inference:
 *   fixed arrays, struct members, pointers, alloca, VLA-like shapes, nested buffers.
 * - DO NOT use manual character-copy loops; they bypass the FORTIFY wrappers.
 *
 * CRITICAL SENTINEL:
 *   printf("SEED_RETURNED\n");
 *   fflush(stdout);
 */

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

  seed(buf_size, fill_size);
  return 0;
}
