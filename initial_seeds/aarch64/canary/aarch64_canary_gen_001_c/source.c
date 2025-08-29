```c
// Seed: A test case targeting VLA bypass and non-linear OOB write on AArch64
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>

struct sensitive_struct {
    char user_buffer[32];
    uint64_t internal_canary;
    void *critical_pointer;
};

void targeted_overflow(int idx, int val, size_t dynamic_size) {
    char local_buf[16];
    struct sensitive_struct s;
    volatile uint64_t *vla_ptr;
    
    // Initialize sensitive data
    s.internal_canary = 0xDEADBEEFCAFEBABE;
    s.critical_pointer = &s;
    
    // Dynamic allocation that comes AFTER canary in stack frame
    vla_ptr = (volatile uint64_t *)alloca(dynamic_size * sizeof(uint64_t));
    
    // Potential non-linear corruption
    volatile int array[8] = {0};
    array[idx] = val;  // OOB write if idx not in [0,7]
    
    // Potential VLA overflow that bypasses current frame's canary
    for (size_t i = 0; i <= dynamic_size + 2; i++) {
        vla_ptr[i] = 0x4141414141414141;  // May corrupt caller's return address
    }
    
    // This should trigger canary check failure if local buffer overflowed
    char small_buf[8];
    memset(small_buf, 'A', 24);  // Linear overflow that should hit canary
}

void caller_function() {
    printf("Caller function executing\n");
    targeted_overflow(10, 0xBAD, 5);  // Trigger OOB write and VLA overflow
    printf("Returned to caller - mitigation may have failed!\n");
}

int main(int argc, char *argv[]) {
    if (argc < 4) {
        printf("Usage: %s <index> <value> <size>\n", argv[0]);
        return 1;
    }
    
    int index = atoi(argv[1]);
    int value = atoi(argv[2]);
    size_t size = atoi(argv[3]);
    
    caller_function();
    
    return 0;
}
```

```makefile
CC = aarch64-linux-gnu-gcc
CFLAGS = -fstack-protector-strong -O2
CFLAGS_UNPROTECTED = -fno-stack-protector -O2
TARGET = test_vla_bypass
TARGET_UNPROTECTED = test_vla_bypass_unprotected

all: $(TARGET) $(TARGET_UNPROTECTED)

$(TARGET): test_vla_bypass.c
	$(CC) $(CFLAGS) -o $@ $<

$(TARGET_UNPROTECTED): test_vla_bypass.c
	$(CC) $(CFLAGS_UNPROTECTED) -o $@ $<

clean:
	rm -f $(TARGET) $(TARGET_UNPROTECTED)

test: $(TARGET) $(TARGET_UNPROTECTED)
	@echo "Testing protected version:"
	-./$(TARGET) 10 123 5 || true
	@echo "Testing unprotected version:"
	-./$(TARGET_UNPROTECTED) 10 123 5 || true

.PHONY: all clean test
```