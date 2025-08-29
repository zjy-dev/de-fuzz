```c
#include <stdio.h>
#include <string.h>
#include <setjmp.h>

jmp_buf env;

void test_partial_canary_null_byte() {
    char buffer[16];
    unsigned long *canary_loc = (unsigned long*)(buffer + 24); // Attempt to target canary position
    
    // Initialize with predictable pattern
    for (int i = 0; i < sizeof(buffer); i++) {
        buffer[i] = 'A';
    }
    
    // Try to overwrite only the least significant byte of canary
    char payload[32];
    memset(payload, 'B', 31);
    payload[31] = '\0'; // Null byte at precise position
    
    strcpy(buffer, payload); // This may partially corrupt the canary
}

void test_arbitrary_index_write(size_t index, char value) {
    char buffer[8];
    // Out-of-bounds write at arbitrary index
    if (index < sizeof(buffer) + 16) { // Allow some out-of-bounds access
        buffer[index] = value; // Could write to canary or return address
    }
}

void test_struct_padding_overflow() {
    struct test_struct {
        char data[12];
        int count;
        // Compiler may insert padding here
        char buffer[8];
    };
    
    struct test_struct s;
    char input[20];
    memset(input, 'C', 19);
    input[19] = '\0';
    
    strcpy(s.buffer, input); // Overflow into potential padding areas
}

int main(int argc, char *argv[]) {
    if (argc < 2) return 1;
    
    // Test different patterns based on input
    if (strcmp(argv[1], "nullbyte") == 0) {
        test_partial_canary_null_byte();
    } else if (strcmp(argv[1], "arbitrary") == 0 && argc > 3) {
        size_t index = atoi(argv[2]);
        char value = argv[3][0];
        test_arbitrary_index_write(index, value);
    } else if (strcmp(argv[1], "struct") == 0) {
        test_struct_padding_overflow();
    }
    
    return 0;
}
```

```makefile
CC=gcc
CFLAGS=-fstack-protector-strong -O0 -g -fno-pie -no-pie
TARGET=stackguard_test

$(TARGET): stackguard_test.c
	$(CC) $(CFLAGS) -o $(TARGET) stackguard_test.c

clean:
	rm -f $(TARGET)

.PHONY: clean
```