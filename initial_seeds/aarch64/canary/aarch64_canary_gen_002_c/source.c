```c
#include <stdio.h>
#include <stdlib.h>
#include <alloca.h>
#include <string.h>
#include <setjmp.h>

jmp_buf env;

struct sensitive {
    char user_buf[32];
    uint64_t internal_canary;
    void *critical_pointer;
};

void targeted_overflow(int offset, int value) {
    int buffer[8];
    volatile uint64_t protected_data = 0xDEADBEEF;
    
    // Index-based OOB write that may skip canary
    buffer[offset] = value;
    
    if (protected_data != 0xDEADBEEF) {
        printf("Data corruption detected!\n");
    }
}

void vla_bypass(size_t dynamic_size) {
    char fixed_buffer[16];
    char *vla_buffer = (char *)alloca(dynamic_size);
    
    // Overflow from VLA - bypasses current function's canary
    memset(vla_buffer, 'B', dynamic_size + 64);
}

void format_string_leak(char *user_input) {
    char local_buf[24];
    // Potential canary leak via format string
    printf(user_input);
    
    // Followed by overflow
    strcpy(local_buf, "This could overflow if input was malicious");
}

void exception_handler_test() {
    char buffer[32];
    
    if (setjmp(env) == 0) {
        // Buffer overflow between setjmp/longjmp
        memset(buffer, 'C', 64);
        longjmp(env, 1);
    }
}

void struct_overflow_test() {
    struct sensitive s;
    s.critical_pointer = &s;
    s.internal_canary = 0xCAFEBABE;
    
    // Overflow within struct - try to bypass internal canary
    strcpy(s.user_buf, "Very long string that might overflow into critical pointer");
    
    if (s.internal_canary != 0xCAFEBABE) {
        printf("Internal canary was corrupted!\n");
    }
}

int main(int argc, char *argv[]) {
    if (argc < 2) {
        printf("Usage: %s <test_case>\n", argv[0]);
        return 1;
    }
    
    int test_case = atoi(argv[1]);
    
    switch(test_case) {
        case 1:
            targeted_overflow(12, 0x41414141); // Potential OOB write
            break;
        case 2:
            vla_bypass(32); // VLA overflow test
            break;
        case 3:
            format_string_leak(argv[2]); // Format string test
            break;
        case 4:
            exception_handler_test(); // setjmp/longjmp test
            break;
        case 5:
            struct_overflow_test(); // Struct internal overflow
            break;
        default:
            printf("Unknown test case\n");
    }
    
    return 0;
}
```

```makefile
CC = aarch64-linux-gnu-gcc
CFLAGS = -fstack-protector-strong -O2
CFLAGS_UNPROTECTED = -fno-stack-protector -O2
SANITIZE_FLAGS = -fsanitize=address

TARGET = canary_test
TARGET_UNPROTECTED = canary_test_unprotected
TARGET_ASAN = canary_test_asan

all: $(TARGET) $(TARGET_UNPROTECTED) $(TARGET_ASAN)

$(TARGET): test_seed.c
	$(CC) $(CFLAGS) -o $@ $<

$(TARGET_UNPROTECTED): test_seed.c
	$(CC) $(CFLAGS_UNPROTECTED) -o $@ $<

$(TARGET_ASAN): test_seed.c
	$(CC) $(CFLAGS) $(SANITIZE_FLAGS) -o $@ $<

clean:
	rm -f $(TARGET) $(TARGET_UNPROTECTED) $(TARGET_ASAN)

.PHONY: all clean
```