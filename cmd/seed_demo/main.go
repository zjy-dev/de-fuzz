package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"defuzz/internal/seed"
)

func main() {
	// Create a temporary directory for demonstration
	demoDir, err := os.MkdirTemp("", "seed_demo_")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(demoDir)

	fmt.Printf("Demo directory: %s\n\n", demoDir)

	// Create example seeds of all three types
	seeds := []*seed.Seed{
		{
			ID:       "demo_c_001",
			Type:     seed.SeedTypeC,
			Content:  "#include <stdio.h>\nint main() {\n    printf(\"Hello from C!\\n\");\n    return 0;\n}",
			Makefile: "all:\n\tgcc source.c -o prog\n\nclean:\n\trm -f prog",
		},
		{
			ID:   "demo_casm_001",
			Type: seed.SeedTypeCAsm,
			Content: `.text
.global main

main:
    # Assembly version of C code (compiled from C, then optimized by LLM)
    mov $1, %rax        # sys_write
    mov $1, %rdi        # stdout
    mov $msg, %rsi      # message
    mov $msg_len, %rdx  # length
    syscall
    
    mov $60, %rax       # sys_exit
    mov $0, %rdi        # exit status
    syscall

.data
msg: .ascii "Hello from C-to-ASM!\n"
msg_len = . - msg`,
			Makefile: "all:\n\tas source.s -64 -o prog.o\n\tld prog.o -o prog\n\nclean:\n\trm -f prog prog.o",
		},
		{
			ID:   "demo_asm_001",
			Type: seed.SeedTypeAsm,
			Content: `.section .text
.global _start

_start:
    # Pure assembly program
    mov $4, %eax        # sys_write (32-bit)
    mov $1, %ebx        # stdout
    mov $message, %ecx  # message
    mov $13, %edx       # length
    int $0x80          # system call
    
    mov $1, %eax        # sys_exit
    mov $0, %ebx        # exit status
    int $0x80          # system call

.section .data
message: .ascii "Hello from ASM!\n"`,
			Makefile: "all:\n\tas --32 source.s -o prog.o\n\tld -m elf_i386 prog.o -o prog\n\nclean:\n\trm -f prog prog.o",
		},
	}

	// Save all seeds
	fmt.Println("Saving seeds...")
	for _, s := range seeds {
		fmt.Printf("  Saving %s (%s)\n", s.ID, s.Type)
		if err := seed.SaveSeed(demoDir, s); err != nil {
			log.Printf("Failed to save seed %s: %v", s.ID, err)
			continue
		}
	}

	// List created directories
	fmt.Printf("\nCreated directories:\n")
	entries, _ := os.ReadDir(demoDir)
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Printf("  %s/\n", entry.Name())

			// List files in each directory
			seedDir := filepath.Join(demoDir, entry.Name())
			files, _ := os.ReadDir(seedDir)
			for _, file := range files {
				fmt.Printf("    - %s\n", file.Name())
			}
		}
	}

	// Load seeds back
	fmt.Printf("\nLoading seeds back...\n")
	pool, err := seed.LoadSeeds(demoDir)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Loaded %d seeds:\n", pool.Len())
	for {
		s := pool.Next()
		if s == nil {
			break
		}
		fmt.Printf("  ID: %s, Type: %s\n", s.ID, s.Type)
		fmt.Printf("    Content preview: %.50s...\n", s.Content)
	}

	// Save understanding
	understanding := `# LLM Understanding

This demonstration shows three types of seeds supported by DeFuzz:

1. **SeedTypeC**: Pure C source code that gets compiled directly
2. **SeedTypeCAsm**: C code that gets compiled to assembly, then optimized by LLM
3. **SeedTypeAsm**: Pure assembly code written directly

Each seed type has different compilation strategies and use cases for fuzzing.`

	fmt.Printf("\nSaving understanding...\n")
	if err := seed.SaveUnderstanding(demoDir, understanding); err != nil {
		log.Printf("Failed to save understanding: %v", err)
	} else {
		fmt.Printf("Understanding saved to %s\n", seed.GetUnderstandingPath(demoDir))
	}

	// Load understanding back
	loadedUnderstanding, err := seed.LoadUnderstanding(demoDir)
	if err != nil {
		log.Printf("Failed to load understanding: %v", err)
	} else {
		fmt.Printf("\nLoaded understanding:\n%s\n", loadedUnderstanding)
	}

	fmt.Printf("\nDemo completed! Check directory: %s\n", demoDir)
}
