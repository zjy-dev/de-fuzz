Source (c):
---
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <stdint.h>

// This program demonstrates a classic stack-based buffer overflow vulnerability
// that could potentially bypass stack canary protection if the canary value
// can be leaked through an information disclosure vulnerability

void leak_canary() {
    char buffer[16];
    // Simulated information disclosure - in real scenario this would be
    // a format string vulnerability or out-of-bounds read
    printf("This function could leak stack contents if vulnerable to format strings\n");
}

void vulnerable_function(const char *input) {
    char buffer[64];  // Small buffer that can be overflowed
    uint64_t *canary_location;
    
    // Stack layout approximation for aarch64:
    // - buffer[64] (local variable)
    // - padding (if needed for alignment)
    // - callee-saved registers (x19-x28)
    // - canary (placed by compiler)
    // - FP' (saved frame pointer)
    // - LR' (return address)
    
    printf("Buffer address: %p\n", buffer);
    
    // This is the vulnerable operation - no bounds checking
    strcpy(buffer, input);
    
    printf("Copied input: %s\n", buffer);
}

int main(int argc, char *argv[]) {
    if (argc < 2) {
        printf("Usage: %s <input_string>\n", argv[0]);
        printf("This program demonstrates a stack buffer overflow vulnerability\n");
        return 1;
    }
    
    // Call function that could leak information
    leak_canary();
    
    // Call vulnerable function with user input
    vulnerable_function(argv[1]);
    
    return 0;
}
---

Makefile:
---
CC = aarch64-linux-gnu-gcc
CFLAGS = -fstack-protector-all -O0 -g -Wall
TARGET = stack_overflow_demo

all: $(TARGET)

$(TARGET): main.c
	$(CC) $(CFLAGS) -o $(TARGET) main.c

clean:
	rm -f $(TARGET) *.o

.PHONY: all clean
---

Run Script (sh):
---
#!/bin/bash

# Build the program
make clean
make all

if [ ! -f "./stack_overflow_demo" ]; then
    echo "Compilation failed!"
    exit 1
fi

echo "=== Running with normal input ==="
./stack_overflow_demo "HelloWorld"

echo -e "\n=== Running with longer input (potential overflow) ==="
# Create a string longer than the 64-byte buffer
LONG_INPUT=$(python3 -c "print('A' * 80)")
./stack_overflow_demo "$LONG_INPUT"

echo -e "\n=== Running with very long input (likely overflow) ==="
VERY_LONG_INPUT=$(python3 -c "print('B' * 200)")
./stack_overflow_demo "$VERY_LONG_INPUT"

echo -e "\n=== Test completed ==="
---