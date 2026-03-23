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
#include <alloca.h>

void seed(int buf_size, int fill_size) {
    // Use a variable-length array (VLA) to trigger different stack layout
    // This may affect the classification bits in stack_protect_decl_phase
    char vla_buffer[buf_size];
    
    // Add a small character array to potentially trigger SPCT_HAS_SMALL_CHAR_ARRAY
    char small_buffer[8];
    
    // Add a large character array to potentially trigger SPCT_HAS_LARGE_CHAR_ARRAY
    char large_buffer[256];
    
    // Fill buffers without bounds checking
    // This will overflow if fill_size > buffer sizes
    memset(vla_buffer, 'A', fill_size);
    memset(small_buffer, 'B', fill_size);
    memset(large_buffer, 'C', fill_size);
    
    // Prevent compiler optimization
    printf("VLA + small + large arrays: filled %d bytes\n", fill_size);

    printf("SEED_RETURNED\n");
    fflush(stdout);
}

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
