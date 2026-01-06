/**
 * Canary Oracle Function Template - Flexible Version
 *
 * Seed 2: Variable-Length Array (VLA) pattern - CVE-2023-4039
 * This pattern uses VLA which bypasses stack canary on AArch64!
 * 
 * On AArch64, the stack canary is placed ABOVE dynamically-sized arrays,
 * leaving the return address vulnerable to overflow.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>

void seed(int buf_size, int fill_size) {
    // Variable-Length Array - uses buf_size for allocation
    // CVE-2023-4039: On AArch64, canary is placed ABOVE VLA
    // Stack layout (vulnerable):
    //   [Canary]     <- Protected but above VLA
    //   [Saved LR]   <- VULNERABLE!
    //   [VLA buffer] <- Overflow starts here
    char vla_buffer[buf_size];
    
    // Fill buffer without bounds checking
    // When fill_size > buf_size, this overflows into return address
    // BEFORE corrupting the canary (bypassing stack protection)
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
