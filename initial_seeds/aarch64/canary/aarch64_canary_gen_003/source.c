Source (c):
---
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <stdint.h>

// Function with vulnerable buffer and format string vulnerability
void vulnerable_function(const char *input, const char *format) {
    char buffer[64];
    uint64_t canary_value;  // This will be placed between buffer and saved registers
    
    // Copy user input to buffer - potential overflow
    strcpy(buffer, input);
    
    // Format string vulnerability - can leak stack values
    printf(format);
    printf("\n");
    
    // The canary check happens here during epilogue
}

int main(int argc, char *argv[]) {
    if (argc < 3) {
        printf("Usage: %s <input> <format>\n", argv[0]);
        printf("Example: %s AAAAAAAA %%p%%p%%p%%p%%p%%p%%p%%p\n", argv[0]);
        return 1;
    }
    
    // Call vulnerable function with user-controlled arguments
    vulnerable_function(argv[1], argv[2]);
    
    return 0;
}
---

Makefile:
---
CC = gcc
CFLAGS = -fstack-protector-all -O0 -g
TARGET = stack_canary_test

all: $(TARGET)

$(TARGET): stack_canary_test.c
	$(CC) $(CFLAGS) -o $(TARGET) stack_canary_test.c

clean:
	rm -f $(TARGET)

.PHONY: all clean
---

Run Script (sh):
---
#!/bin/bash

# Build the target
make clean
make all

echo "Running stack canary test with demonstration inputs..."
echo ""

# Test 1: Normal operation with short input
echo "Test 1: Normal input"
./stack_canary_test "Hello" "Normal format: %s"

echo ""
echo "Test 2: Attempt to leak stack values (including potential canary)"
echo "Test 2: Stack value leak attempt"
./stack_canary_test "AAAAAAAA" "%p.%p.%p.%p.%p.%p.%p.%p.%p.%p"

echo ""
echo "Test 3: Buffer overflow attempt"
echo "Test 3: Overflow test (may crash with stack protection)"
./stack_canary_test "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" "Overflow test: %s"

echo ""
echo "Test 4: Combined attack - overflow with format string"
echo "Test 4: Combined attack attempt"
./stack_canary_test "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" "%p.%p.%p.%p.%p.%p"

echo ""
echo "All tests completed."
---