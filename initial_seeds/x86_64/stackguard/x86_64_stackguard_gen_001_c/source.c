```c
// Pattern 2A: Arbitrary Write / Index Corruption
// Tests non-linear corruption by allowing controlled index/value writes
#include <stdio.h>
#include <string.h>

void targeted_corruption(size_t index, char value) {
    char buffer[24];
    volatile char probe;
    
    // Initialize buffer with pattern for better observability
    memset(buffer, 0x41, sizeof(buffer));
    
    // Perform targeted write at arbitrary index
    if (index < sizeof(buffer) + 32) {  // Allow some out-of-bounds access
        buffer[index] = value;
    }
    
    // Access probe to prevent optimization
    probe = buffer[0];
}

int main(int argc, char *argv[]) {
    if (argc < 3) {
        printf("Usage: %s <index> <value>\n", argv[0]);
        return 1;
    }
    
    size_t index = atoi(argv[1]);
    char value = argv[2][0];
    
    targeted_corruption(index, value);
    return 0;
}
```

```makefile
# Makefile for StackGuard fuzzing test
CC=gcc
CFLAGS=-fstack-protector-strong -O0 -g
TARGET=arbitrary_write_test

$(TARGET): arbitrary_write_test.c
	$(CC) $(CFLAGS) -o $(TARGET) arbitrary_write_test.c

clean:
	rm -f $(TARGET)

.PHONY: clean
```