#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <stdlib.h>

struct container {
    char data[32];
    int count;
};

void vulnerable_function() {
    char buffer[64];
    struct container c;
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
    
    // Stack buffer overflow through struct
    if(strlen(buffer) > 32) {
        memcpy(c.data, buffer, strlen(buffer));  // Potential overflow into canary
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

int main(int argc, char *argv[]) {
    printf("Stack Canary Test Program - Struct Variant\n");
    printf("Canary value: 0x1234567890ABCDEF\n");
    
    if(argc > 1) {
        int size = atoi(argv[1]);
        helper_function(size);
    }
    
    vulnerable_function();
    
    printf("Program completed successfully\n");
    return 0;
}