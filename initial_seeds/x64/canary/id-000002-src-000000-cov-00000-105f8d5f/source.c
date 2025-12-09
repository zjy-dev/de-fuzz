/**
 * Canary Oracle Function Template
 *
 * This template is used for testing stack canary protection mechanisms.
 * The LLM generates only the seed() function body.
 *
 * Usage: ./prog <buffer_size>
 *   - buffer_size: Number of 'A' characters to write into the buffer
 *   - Example: ./prog 100 writes 100 'A's into the buffer
 *
 * Expected behavior:
 *   - Small sizes: Program exits normally (return 0)
 *   - Medium sizes (canary overwritten): SIGABRT (exit code 134)
 *   - Large sizes (ret addr overwritten without canary): SIGSEGV (exit code
 * 139)
 *
 * The canary oracle uses binary search on buffer_size to detect
 * vulnerabilities.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void seed(int fill_size) {
    char buffer[64];
    char *ptr = buffer;
    int i = 0;
    
    // Vulnerable loop with no bounds checking
    while (i < fill_size) {
        *ptr++ = 'A';
        i++;
    }
    
    // Additional vulnerable operation that could leak canary
    if (fill_size > 32 && fill_size < 96) {
        // This might print memory beyond buffer including canary
        printf("Debug: %s\n", buffer);
    }
}

// Disable stack protector for main to maximize attack surface
// This ensures canary check only happens in seed() if at all
#define NO_CANARY __attribute__((no_stack_protector))

NO_CANARY int main(int argc, char *argv[]) {
  if (argc != 2) {
    fprintf(stderr, "Usage: %s <buffer_size>\n", argv[0]);
    return 1;
  }

  int fill_size = atoi(argv[1]);
  if (fill_size < 0) {
    fprintf(stderr, "Error: buffer_size must be non-negative\n");
    return 1;
  }

  // Call the seed function with the specified fill size
  seed(fill_size);

  return 0;
}
