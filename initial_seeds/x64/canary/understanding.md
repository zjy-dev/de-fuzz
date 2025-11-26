## Goal
Your goal is to generate and mutate C code to discover vulnerabilities in the 'canary' defense strategy on the 'x64' architecture. You must create programs that can bypass, corrupt, leak, or otherwise defeat stack canary protection mechanisms.

## Core Concepts

**Stack Canary Defense:**
- A stack canary is a random value placed on the stack between local variables and the return address
- The compiler inserts canary checks before function returns to verify the canary hasn't been modified
- On x64, canaries are typically 8-byte values stored in the %fs segment (e.g., %fs:0x28)
- Common implementations: Terminator canaries (contain null bytes, CR, LF, EOF), random canaries, XOR canaries
- The protection works by detecting stack buffer overflows before they can overwrite the return address

**x64 Stack Layout (Typical with Canary):**
```
[High Address]
Return Address
Saved Frame Pointer (RBP)
Stack Canary
Local Variables
[Low Address]
```

**Theoretical Weaknesses:**
- Canary value leakage through format strings, out-of-bounds reads, or side channels
- Non-random or predictable canary generation
- Logic bugs that bypass the canary check entirely
- Canary destruction before the check occurs
- Improper canary implementation in specific code patterns

## Attack Vectors & Vulnerability Patterns

*   **Canary Leakage via Format String Vulnerabilities:**
    ```c
    char buffer[64];
    gets(buffer); // Or other unsafe input
    printf(buffer); // Leaks canary if format specifiers are provided
    ```

*   **Out-of-Bounds Read to Leak Canary:**
    ```c
    int array[10];
    // Read beyond array bounds to access canary
    for(int i = 0; i < 20; i++) {
        printf("array[%d] = %x\n", i, array[i]);
    }
    ```

*   **Stack Pivoting to Bypass Canary Check:**
    ```c
    void vulnerable() {
        char buf[64];
        read(0, buf, 256); // Overflow
        // Canary check happens here but we've already pivoted
    }
    ```

*   **longjmp/setjmp Canary Bypass:**
    ```c
    jmp_buf env;
    void func() {
        char buf[64];
        if(setjmp(env) != 0) {
            return; // Bypasses normal return path and canary check
        }
        gets(buf); // Overflow here
        longjmp(env, 1);
    }
    ```

*   **Exception Handling Bypass:**
    ```c
    void vulnerable() {
        char buf[64];
        gets(buf);
        throw std::exception(); // Unwinds stack without canary check
    }
    ```

*   **Multi-threaded Canary Corruption:**
    ```c
    char *shared_buffer;
    void thread1() {
        strcpy(shared_buffer, "very long string...");
    }
    void thread2() {
        char local[64];
        // Race condition: thread1 overwrites thread2's canary
    }
    ```

*   **Pointer Arithmetic Confusion:**
    ```c
    struct container {
        char data[64];
        // Compiler might place canary here in some implementations
    };
    void func() {
        struct container c;
        char *ptr = &c.data[64]; // Points to canary location
        *ptr = 0; // Direct canary modification
    }
    ```

*   **Variable Length Array Canary Placement Bugs:**
    ```c
    void func(int size) {
        char fixed[64];
        char vla[size];
        // Compiler might misplace canary between fixed and VLA
        memset(vla, 'A', size + 100); // Corrupt canary
    }
    ```

*   **Stack Frame Reuse Attacks:**
    ```c
    void func1() {
        char buf[64];
        gets(buf); // Leaks/corrupts canary
    }
    void func2() {
        // Reuses same stack frame, might inherit corrupted state
        vulnerable_operation();
    }
    ```

*   **Signal Handler Canary Corruption:**
    ```c
    void handler(int sig) {
        char buf[256];
        // Signal handler runs on same stack, can corrupt canary
        memset(buf, 0, 512);
    }
    ```

*   **Alloca-based Canary Bypass:**
    ```c
    void func(int size) {
        char *buf = alloca(size);
        char fixed[64];
        // alloca might affect canary placement
        memset(buf, 'A', size + 72); // Overwrite canary
    }
    ```

## Seed Generation & Mutation Rules

**Output Format Requirements:**
- Always generate a complete, compilable C source file
- Include a Makefile with appropriate compiler flags (-fstack-protector-all, -fstack-protector-strong, etc.)
- Provide a run.sh script that compiles and executes the test case
- Each output must be self-contained and ready to test

**Mutation Guidelines:**
- Make small, targeted changes to existing code patterns
- Focus on one vulnerability class per mutation
- Vary buffer sizes, loop bounds, and pointer arithmetic
- Experiment with different control flow patterns
- Test edge cases: zero-length buffers, maximum sizes, negative indices
- Combine multiple vulnerability patterns in novel ways
- Vary compiler optimization levels (-O0, -O1, -O2, -Os)
- Test with different stack protector modes

**Code Quality Requirements:**
- Include necessary headers (#include <stdio.h>, etc.)
- Ensure code compiles without syntax errors
- Add comments explaining the attack methodology
- Test for both GCC and Clang compatibility
- Include error checking where appropriate