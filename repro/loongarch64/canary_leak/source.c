/*
 * Standalone LoongArch64 canary-leak reproducer (freestanding).
 *
 * Goal
 * ----
 * Exercise the de-fuzz canary oracle (`internal/oracle`) end-to-end on a
 * single, self-contained source file without going through the LLM /
 * fuzz-loop pipeline. The same scrub-probe machinery that
 * `initial_seeds/loongarch64/canary/function_template.c` ships is inlined
 * here; the only addition is a trivial `seed()` body that triggers
 * `-fstack-protector-strong` instrumentation.
 *
 * Why freestanding
 * ----------------
 * The local cross-toolchain at
 *   target_compilers/gcc-v16.1.0-loongarch64-cross-compile/install-...
 * has a working `xgcc` + binutils, but its glibc build never finished
 * (BFD assertion in elfnn-loongarch.c when linking static-pie support
 * binaries). Rather than rebuild glibc, we link `-nostdlib -static`
 * against syscall stubs we provide ourselves. The oracle only looks at
 * stdout markers (`GUARD_LEAKED`, `CANARY_SCRUB_OK`) and exit codes, both
 * of which we can produce with `write(2)` and `exit_group(2)` syscalls.
 *
 * We intentionally define `__stack_chk_guard` ourselves with a known
 * sentinel value (0xDEADBEEFCAFEBABE). On loongarch64-unknown-linux-gnu
 * with GCC 16.1.0, the generated epilogue loads the guard via
 *   la.global $r12, __stack_chk_guard
 *   ldptr.d  $r12, $r12, 0
 *   beq      $r13, $r12, .L_ok
 *   ...
 *   .L_ok:    jr $r1
 * leaving the guard value live in `$r12` (= `$t0`, caller-saved) across
 * the return — this is the leak channel INV-SP-R03 was written for.
 *
 * Build
 * -----
 *   $XGCC \
 *     -nostdlib -nostartfiles -ffreestanding -static -no-pie \
 *     -O0 -fstack-protector-strong \
 *     -fno-asynchronous-unwind-tables -fno-unwind-tables \
 *     -o canary_leak source.c
 *
 * Run (scrub mode, the one INV-SP-R03 inspects):
 *   qemu-loongarch64 ./canary_leak scrub
 * Expected stdout on a vulnerable compiler:
 *   GUARD_LEAKED reg=9 name=t0
 *
 * Run (binary-search mode, INV-SP-L01):
 *   qemu-loongarch64 ./canary_leak 64 256
 * Expected: stack smashing detected → exit 134 (SIGABRT translation).
 */

#include <stdint.h>
#include <stddef.h>

/* ------------------------------------------------------------------ */
/*  __stack_chk_guard / __stack_chk_fail (normally provided by glibc) */
/* ------------------------------------------------------------------ */

/* A unique 8-byte value the scrub probe can recognize unambiguously
 * inside any caller-saved register. NOT exported as weak: we want our
 * definition to win at link time. */
__attribute__((aligned(8), used))
uintptr_t __stack_chk_guard = (uintptr_t)0xDEADBEEFCAFEBABEULL;

#define NO_CANARY __attribute__((no_stack_protector))

/* ------------------------------------------------------------------ */
/*  Minimal syscall layer (LoongArch64 Linux)                         */
/*  argument regs: $a0..$a7 = $r4..$r11; syscall # in $a7 = $r11.     */
/* ------------------------------------------------------------------ */

NO_CANARY static long la_syscall1(long n, long a) {
    register long r4  __asm__("$r4")  = a;
    register long r11 __asm__("$r11") = n;
    __asm__ volatile ("syscall 0"
                      : "+r"(r4)
                      : "r"(r11)
                      : "memory");
    return r4;
}

NO_CANARY static long la_syscall3(long n, long a, long b, long c) {
    register long r4  __asm__("$r4")  = a;
    register long r5  __asm__("$r5")  = b;
    register long r6  __asm__("$r6")  = c;
    register long r11 __asm__("$r11") = n;
    __asm__ volatile ("syscall 0"
                      : "+r"(r4)
                      : "r"(r5), "r"(r6), "r"(r11)
                      : "memory");
    return r4;
}

NO_CANARY __attribute__((noreturn))
static void sys_exit(int code) {
    /* exit_group = 94 on every Linux ABI we care about */
    la_syscall1(94, code);
    __builtin_unreachable();
}

NO_CANARY static long sys_write(int fd, const void *buf, size_t n) {
    /* write = 64 */
    return la_syscall3(64, fd, (long)buf, (long)n);
}

/* ------------------------------------------------------------------ */
/*  Tiny libc shims (write, strlen, strcmp, atoi, memset, int->dec)   */
/* ------------------------------------------------------------------ */

NO_CANARY static size_t mini_strlen(const char *s) {
    size_t n = 0;
    while (s[n]) n++;
    return n;
}

NO_CANARY static int mini_strcmp(const char *a, const char *b) {
    while (*a && *a == *b) { a++; b++; }
    return (int)(unsigned char)*a - (int)(unsigned char)*b;
}

NO_CANARY static int mini_atoi(const char *s) {
    int sign = 1, v = 0;
    if (*s == '-') { sign = -1; s++; }
    else if (*s == '+')          s++;
    while (*s >= '0' && *s <= '9') {
        v = v * 10 + (*s - '0');
        s++;
    }
    return v * sign;
}

NO_CANARY static void print_str(const char *s) {
    sys_write(1, s, mini_strlen(s));
}

NO_CANARY static void print_err(const char *s) {
    sys_write(2, s, mini_strlen(s));
}

NO_CANARY static void print_long(long v) {
    char buf[24];
    int  i = (int)sizeof buf;
    unsigned long uv = (v < 0) ? (unsigned long)(-v) : (unsigned long)v;
    do {
        buf[--i] = (char)('0' + (int)(uv % 10));
        uv /= 10;
    } while (uv);
    if (v < 0) buf[--i] = '-';
    sys_write(1, &buf[i], (size_t)((int)sizeof buf - i));
}

/* GCC may emit calls to `memset` from -fstack-protector code paths and
 * from the `seed()` body's array fill. Provide our own; mark NO_CANARY so
 * we don't recursively re-enter the canary check while writing the
 * canary slot. */
NO_CANARY void *memset(void *dst, int c, size_t n) {
    unsigned char *p = (unsigned char *)dst;
    for (size_t i = 0; i < n; i++) p[i] = (unsigned char)c;
    return dst;
}

/* The hook the compiler calls when the canary check fails. Must be
 * NO_CANARY to avoid infinite recursion on a corrupt frame. */
NO_CANARY __attribute__((noreturn))
void __stack_chk_fail(void) {
    static const char m[] = "*** stack smashing detected ***\n";
    sys_write(2, m, sizeof m - 1);
    sys_exit(134);  /* mirrors SIGABRT exit code (128 + 6) */
}

/* ------------------------------------------------------------------ */
/*  Seed function (the function under test).                          */
/*                                                                    */
/*  Compiled with -fstack-protector-strong, the 64-byte char buffer   */
/*  forces canary insertion. `noinline` + `used` ensure GCC keeps an  */
/*  independent epilogue containing the guard load+compare; without   */
/*  these, the inliner could merge seed into run_scrub_probe and      */
/*  obscure the leak channel.                                         */
/* ------------------------------------------------------------------ */

__attribute__((noinline, used))
void seed(int buf_size, int fill_size) {
    (void)buf_size;
    char buffer[64];
    memset(buffer, 'A', (size_t)fill_size);
    print_str("seed: filled ");
    print_long(fill_size);
    print_str(" bytes into 64-byte buffer\n");
    print_str("SEED_RETURNED\n");
}

/* ------------------------------------------------------------------ */
/*  Scrub probe (INV-SP-R03 ground truth).                            */
/*                                                                    */
/*  Mirrors initial_seeds/loongarch64/canary/function_template.c      */
/*  exactly so the oracle's parser sees the canonical markers.        */
/* ------------------------------------------------------------------ */

#define SCRUB_N_LOONGARCH64 18

NO_CANARY static void run_scrub_probe(int buf_size, int fill_size) {
    static const char *const SCRUB_NAMES[SCRUB_N_LOONGARCH64] = {
        "ra",
        "a0", "a1", "a2", "a3", "a4", "a5", "a6", "a7",
        "t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8",
    };
    uintptr_t snap[SCRUB_N_LOONGARCH64] = {0};
    register uintptr_t *snap_ptr __asm__("$r23") = snap;
    register int        bs_in    __asm__("$r4")  = buf_size;
    register int        fs_in    __asm__("$r5")  = fill_size;

    __asm__ volatile (
        "bl seed\n\t"
        "st.d $r1,  $r23,   0\n\t"   /* ra */
        "st.d $r4,  $r23,   8\n\t"   /* a0 */
        "st.d $r5,  $r23,  16\n\t"   /* a1 */
        "st.d $r6,  $r23,  24\n\t"   /* a2 */
        "st.d $r7,  $r23,  32\n\t"   /* a3 */
        "st.d $r8,  $r23,  40\n\t"   /* a4 */
        "st.d $r9,  $r23,  48\n\t"   /* a5 */
        "st.d $r10, $r23,  56\n\t"   /* a6 */
        "st.d $r11, $r23,  64\n\t"   /* a7 */
        "st.d $r12, $r23,  72\n\t"   /* t0  <-- INV-SP-R03 leak slot */
        "st.d $r13, $r23,  80\n\t"   /* t1 */
        "st.d $r14, $r23,  88\n\t"   /* t2 */
        "st.d $r15, $r23,  96\n\t"   /* t3 */
        "st.d $r16, $r23, 104\n\t"   /* t4 */
        "st.d $r17, $r23, 112\n\t"   /* t5 */
        "st.d $r18, $r23, 120\n\t"   /* t6 */
        "st.d $r19, $r23, 128\n\t"   /* t7 */
        "st.d $r20, $r23, 136\n\t"   /* t8 */
        : "+r"(snap_ptr)
        : "r"(bs_in), "r"(fs_in)
        : "$r1", "$r6", "$r7", "$r8", "$r9", "$r10", "$r11",
          "$r12", "$r13", "$r14", "$r15", "$r16", "$r17", "$r18",
          "$r19", "$r20",
          "memory"
    );

    uintptr_t guard = __stack_chk_guard;
    for (int i = 0; i < SCRUB_N_LOONGARCH64; i++) {
        if (snap[i] == guard) {
            print_str("GUARD_LEAKED reg=");
            print_long(i);
            print_str(" name=");
            print_str(SCRUB_NAMES[i]);
            print_str("\n");
            sys_exit(1);
        }
    }
    print_str("CANARY_SCRUB_OK\n");
    sys_exit(0);
}

/* ------------------------------------------------------------------ */
/*  main + _start                                                     */
/* ------------------------------------------------------------------ */

NO_CANARY int main(int argc, char *argv[]) {
    if (argc == 2 && mini_strcmp(argv[1], "scrub") == 0) {
        run_scrub_probe(64, 0);
        return 1;  /* unreachable */
    }
    if (argc != 3) {
        print_err("Usage: <prog> <buf_size> <fill_size>\n");
        print_err("   or: <prog> scrub\n");
        return 1;
    }
    int buf_size  = mini_atoi(argv[1]);
    int fill_size = mini_atoi(argv[2]);
    if (buf_size < 0 || fill_size < 0) {
        print_err("error: sizes must be non-negative\n");
        return 1;
    }
    seed(buf_size, fill_size);
    return 0;
}

/* Kernel entry point. The Linux ELF loader places (argc, argv[0..argc-1],
 * NULL, envp...) at the top of the initial stack. We pass argc/argv to
 * main, then exit_group with main's return value.
 *
 * GCC's loongarch backend ignores `__attribute__((naked))`, so we emit
 * `_start` as raw assembly at file scope to avoid GCC inserting a
 * prologue that would clobber the kernel-supplied $sp before we can
 * read argc. */
__asm__ (
    ".text\n\t"
    ".globl _start\n\t"
    ".type  _start, @function\n"
    "_start:\n\t"
    "    ld.d   $a0, $sp, 0\n\t"      /* argc                 */
    "    addi.d $a1, $sp, 8\n\t"      /* argv = sp + 8        */
    "    bl     main\n\t"
    "    li.d   $a7, 94\n\t"          /* exit_group           */
    "    syscall 0\n\t"
    "1:  b      1b\n\t"
    ".size _start, .-_start\n"
);
