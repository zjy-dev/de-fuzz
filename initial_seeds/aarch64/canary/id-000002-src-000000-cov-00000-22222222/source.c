/**
 * Canary Oracle Function Template - Flexible Version
 *
 * Seed 2: Variable-Length Array (VLA) pattern
 * This pattern uses VLA for dynamic stack allocation.
 * 
 * On AArch64, VLA has a different stack layout than fixed-size arrays,
 * which affects how stack protection mechanisms work.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>

void seed(int buf_size, int fill_size) {
    // Variable-Length Array - uses buf_size for allocation
    // Stack layout with VLA differs from fixed arrays
    char vla_buffer[buf_size];
    
    // Fill buffer without bounds checking
    // When fill_size > buf_size, this causes overflow
    memset(vla_buffer, 'A', fill_size);
    
    // Prevent compiler optimization
    printf("VLA: filled %d bytes into %d-byte buffer\n", fill_size, buf_size);
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
