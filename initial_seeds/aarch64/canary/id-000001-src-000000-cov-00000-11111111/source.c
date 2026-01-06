/**
 * Canary Oracle Function Template - Flexible Version
 *
 * Seed 1: Fixed-size array pattern
 * This pattern uses a fixed 64-byte buffer regardless of buf_size.
 * Stack protector should work correctly for this pattern.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>

void seed(int buf_size, int fill_size) {
    // Fixed-size array - ignores buf_size
    // Stack protector places canary BELOW buffer (correct protection)
    char buffer[64];
    
    // Fill buffer without bounds checking
    // This will overflow if fill_size > 64
    memset(buffer, 'A', fill_size);
    
    // Prevent compiler optimization
    printf("Fixed array: filled %d bytes into 64-byte buffer\n", fill_size);
}

// Disable stack protector for main to maximize attack surface
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
