#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <stdlib.h>

struct container {
    char data[32];
    int count;
};

union mixed_container {
    char buffer[16];
    int numbers[4];
    struct {
        char flag;
        char padding[15];
    } flags;
};

void vulnerable_function() {
    char buffer[64];
    struct container c;
    union mixed_container u;
    unsigned long canary = 0x1234567890ABCDEF;
    
    printf("Enter input: ");
    fflush(stdout);
    
    // Use fgets instead of gets for safer input, but with wrong size
    fgets(buffer, 128, stdin);  // Wrong size - larger than buffer
    
    // Format string vulnerability with array access
    printf("You entered: ");
    printf(buffer);
    printf("\n");
    
    // Out-of-bounds read through struct array
    printf("Struct contents: ");
    for(int i = 0; i < 40; i++) {  // Read beyond struct bounds
        printf("%02x ", ((unsigned char*)&c)[i]);
    }
    printf("\n");
    
    // Union access pattern that might confuse stack protector
    printf("Union contents: ");
    for(int i = 0; i < 20; i++) {  // Read beyond union bounds
        printf("%02x ", ((unsigned char*)&u)[i]);
    }
    printf("\n");
    
    // Stack buffer overflow through struct
    if(strlen(buffer) > 32) {
        memcpy(c.data, buffer, strlen(buffer));  // Potential overflow into canary
    }
    
    // Union-based overflow attempt
    if(strlen(buffer) > 16) {
        memcpy(u.buffer, buffer, strlen(buffer));  // Overflow within union
    }
    
    // Canary check simulation
    if(canary != 0x1234567890ABCDEF) {
        printf("*** stack smashing detected ***\n");
        _exit(1);
    }
}

void helper_function(int size) {
    char local_buf[16];
    char *dynamic_buf = alloca(size);
    
    printf("Helper function - size: %d\n", size);
    
    // Potential alloca overflow affecting stack layout
    if(dynamic_buf && size > 0) {
        memset(dynamic_buf, 'B', size + 8);  // May overflow into canary region
    }
}

void union_overflow_test() {
    union {
        char small[8];
        char large[64];
    } u;
    
    printf("Union overflow test - enter data: ");
    fflush(stdout);
    
    char input[128];
    fgets(input, sizeof(input), stdin);
    
    // Copy to union small buffer but using large buffer size
    memcpy(u.large, input, strlen(input));  // Overflow within union
    
    printf("Union contents after copy: ");
    for(int i = 0; i < 70; i++) {  // Read beyond union bounds
        printf("%02x ", ((unsigned char*)&u)[i]);
    }
    printf("\n");
}

int main(int argc, char *argv[]) {
    printf("Stack Canary Test Program - Union/Struct Variant\n");
    printf("Canary value: 0x1234567890ABCDEF\n");
    
    if(argc > 1) {
        int size = atoi(argv[1]);
        helper_function(size);
    }
    
    if(argc > 2 && strcmp(argv[2], "union") == 0) {
        union_overflow_test();
    }
    
    vulnerable_function();
    
    printf("Program completed successfully\n");
    return 0;
}