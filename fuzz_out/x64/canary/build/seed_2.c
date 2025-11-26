#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <setjmp.h>

jmp_buf env;

void vulnerable_function() {
    char buffer[64];
    char canary_guard[8];
    
    // Simulate stack canary placement
    unsigned long canary = 0x1234567890ABCDEF;
    
    printf("Enter input: ");
    fflush(stdout);
    
    if(setjmp(env) != 0) {
        printf("longjmp bypass activated - skipping canary check!\n");
        return; // Bypasses normal return path and canary check
    }
    
    // Vulnerable gets() - no bounds checking
    gets(buffer);
    
    // Format string vulnerability - can leak stack contents including canary
    printf("You entered: ");
    printf(buffer); // Direct format string vulnerability
    printf("\n");
    
    // Stack buffer overflow attempt
    if(strlen(buffer) > 64) {
        printf("Buffer overflow detected! Attempting longjmp bypass...\n");
        longjmp(env, 1); // Bypass canary check via longjmp
    }
    
    // Canary check simulation (will be skipped if longjmp is used)
    if(canary != 0x1234567890ABCDEF) {
        printf("*** stack smashing detected ***\n");
        _exit(1);
    }
}

void second_vulnerable_function() {
    char small_buf[8];
    char large_buf[128];
    
    // Different buffer sizes to test various stack protector classifications
    printf("Enter second input: ");
    fflush(stdout);
    
    gets(small_buf); // Potential overflow into large_buf or canary
    
    // Test array bounds checking
    for(int i = 0; i < 20; i++) {
        printf("small_buf[%d] = 0x%02x\n", i, (unsigned char)small_buf[i]);
    }
}

int main(int argc, char *argv[]) {
    printf("Stack Canary Test Program - longjmp Bypass Variant\n");
    printf("Canary value: 0x1234567890ABCDEF\n");
    
    vulnerable_function();
    second_vulnerable_function();
    
    printf("Program completed successfully\n");
    return 0;
}