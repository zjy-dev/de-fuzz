Source (c):
---
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

// Function with vulnerable buffer and format string vulnerability
void vulnerable_function(const char *input, const char *format) {
    char buffer[64];
    long canary_value;
    
    // Simulate canary placement - in reality this would be done by compiler
    // This is placed between local variables and saved registers
    volatile long *fake_canary = (long *)((char *)&canary_value - 8);
    *fake_canary = 0xdeadbeefcafebabe;  // Simulated canary value
    
    printf("Buffer address: %p\n", buffer);
    printf("Fake canary address: %p\n", fake_canary);
    
    // Format string vulnerability - can leak stack values
    printf(format);
    printf("\n");
    
    // Buffer overflow vulnerability
    strcpy(buffer, input);
    
    // Simulated canary check
    if (*fake_canary != 0xdeadbeefcafebabe) {
        printf("*** stack smashing detected ***\n");
        exit(1);
    }
    
    printf("Function completed successfully\n");
}

// Helper function to demonstrate control flow hijack
void target_function() {
    printf("*** EXPLOIT SUCCESS: Control flow hijacked! ***\n");
    exit(0);
}

int main(int argc, char *argv[]) {
    if (argc < 3) {
        printf("Usage: %s <input> <format>\n", argv[0]);
        printf("Example: %s AAAA %p%p%p%p%p%p%p%p%p%p\n", argv[0]);
        return 1;
    }
    
    printf("Starting vulnerable program...\n");
    vulnerable_function(argv[1], argv[2]);
    
    return 0;
}
---

Makefile:
---
CC = gcc
CFLAGS = -fstack-protector-all -O0 -g -Wall
TARGET = vulnerable_program

all: $(TARGET)

$(TARGET): vulnerable_program.c
	$(CC) $(CFLAGS) -o $(TARGET) vulnerable_program.c

clean:
	rm -f $(TARGET)

.PHONY: all clean
---

Run Script (sh):
---
#!/bin/bash

# Build the program
make clean
make all

echo "=== Testing normal operation ==="
./vulnerable_program "normal_input" "Normal format"

echo -e "\n=== Testing format string vulnerability ==="
# Attempt to leak stack values including potential canary
./vulnerable_program "test" "%p.%p.%p.%p.%p.%p.%p.%p.%p.%p"

echo -e "\n=== Testing buffer overflow ==="
# Create a long input that might overflow buffer
python3 -c "print('A'*100)" | xargs -0 ./vulnerable_program "short_format"

echo -e "\n=== Testing combined attack ==="
# Try to combine format string and buffer overflow
./vulnerable_program "$(python3 -c "print('A'*80)")" "%p%p%p%p%p%p%p%p"

echo -e "\n=== Testing with special pattern ==="
# Use pattern that might help identify canary location
./vulnerable_program "AAAA_BBBB_CCCC_DDDD_EEEE" "Format_%p_%p_%p"
---