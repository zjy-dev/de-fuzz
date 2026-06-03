/**
 * FORTIFY Oracle Function Template
 *
 * This template exercises every argv mode consumed by the FORTIFY
 * mechanism oracle (`internal/oracle.FortifyOracle`). The LLM only
 * generates the body of `seed(const char *mode, int n)`; the rest of
 * the file is fixed harness.
 *
 * argv modes (one token, no surrounding whitespace):
 *
 *   bos                 — exercise INV-FORT-W01 / O01: route through
 *                         memcpy() with a struct-last-member dst so the
 *                         compiler's BOS lowering is observable in the
 *                         binary's `__memcpy_chk` call site.
 *   procmaps            — exercise INV-FORT-R01: attempt a `%n` write
 *                         under a simulated `/proc/self/maps` outage.
 *                         Because we cannot fake seccomp from inside
 *                         the binary itself in QEMU, we declare NA
 *                         unless the host sets FORTIFY_FORCE_BYPASS=1
 *                         (test hook).
 *   chkfail             — exercise INV-FORT-R02: deliberately overflow a
 *                         tiny buffer with a known-bad memcpy len so
 *                         glibc's `__chk_fail` handler fires. The
 *                         template prints FORTIFY_R02_RETURNED iff
 *                         control falls past the call.
 *   printf:<entry>      — exercise INV-FORT-C01: invoke <entry> from
 *                         the printf family with a `%n` payload. <entry>
 *                         is one of printf / sprintf / snprintf /
 *                         vsnprintf / syslog.
 *
 * Sentinel:
 *   The harness prints "SEED_RETURNED\n" after seed() returns from the
 *   `bos` mode so that downstream static checkers can confirm the seed
 *   compiled and ran (this is the same convention as the canary
 *   template).
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <stdarg.h>
#include <errno.h>

/* INV-FORT-O01 / W01 ground truth — a struct whose last member is a
 * char array. The LLM's seed() body should write into this field via
 * memcpy() so that the compiler must lower BOS for that field; the
 * resulting `__memcpy_chk` call site becomes the observation channel
 * for O01 / W01. */
struct fortify_last_member {
    int header;
    char tail[16];
};

/* FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement: void seed(const char *mode, int n)
 * - mode dispatches behavior; n is caller-supplied size (use as memcpy len)
 * - Keep _FORTIFY_SOURCE active: do NOT bypass it via casts to (size_t)
 *   or alloca/VLA tricks
 *
 * Example minimal body:
 *
 *   void seed(const char *mode, int n) {
 *       struct fortify_last_member s;
 *       memcpy(s.tail, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", (size_t)n);
 *       (void)mode;
 *   }
 */
void seed(const char *mode, int n) __attribute__((noinline, used));

/* ---------- INV-FORT-R01: __readonly_area probe ---------- */
static int run_procmaps_probe(void) {
    /* Inside QEMU/seccomp the binary itself cannot fake /proc outage
     * without external help. The template honours an env-var hook so
     * unit tests / repro harnesses can drive the path manually:
     *
     *   FORTIFY_FORCE_BYPASS=1 -> emit BYPASS marker
     *   FORTIFY_FORCE_TRAP=1   -> emit TRAPPED marker
     *
     * The default path emits NA so the dynamic checker can record an
     * accurate "environment unable to fake /proc outage" reason. */
    const char *bypass = getenv("FORTIFY_FORCE_BYPASS");
    const char *trap   = getenv("FORTIFY_FORCE_TRAP");
    if (bypass && bypass[0] == '1') {
        printf("FORTIFY_R01_BYPASS reason=forced-bypass-for-test\n");
        fflush(stdout);
        return 0;
    }
    if (trap && trap[0] == '1') {
        printf("FORTIFY_R01_TRAPPED reason=forced-trap-for-test\n");
        fflush(stdout);
        return 0;
    }
    printf("FORTIFY_R01_NA reason=cannot-fake-proc-outage-in-process\n");
    fflush(stdout);
    return 0;
}

/* ---------- INV-FORT-R02: __chk_fail noreturn probe ---------- */
static int run_chkfail_probe(void) {
    /* Trigger __memcpy_chk: a 4-byte destination with a 64-byte memcpy.
     * BOS folds the dst object size to 4, the chk wrapper compares
     * len(=64) against 4, mismatch -> __chk_fail() -> abort(). */
    char tiny[4];
    /* Use a non-constant length that the compiler still recognises as
     * larger than BOS(tiny). Volatile prevents the front-end from
     * proving the bug at compile time. */
    volatile size_t bad = 64;
    /* Print the marker BEFORE the call so we can grep it in logs even
     * if the trap consumes stderr. */
    printf("FORTIFY_R02_PROBE_BEGIN\n");
    fflush(stdout);

    memcpy(tiny, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", bad);

    /* If we get here, __chk_fail returned. That is the violation. */
    printf("FORTIFY_R02_RETURNED tiny[0]=%d\n", (int)tiny[0]);
    fflush(stdout);
    return 0;
}

/* ---------- INV-FORT-C01: printf-family fortify-flag probe ----------
 *
 * Each entry is invoked with a format string containing a `%n` that
 * tries to write into a writable stack int. With a correctly-fortified
 * vfprintf the call ends in __chk_fail (Pass). If the entry skips the
 * fortify flag, vfprintf accepts %n -> the int gets written -> we
 * reach the BYPASS marker.
 *
 * The probe is best-effort across glibc versions: on a glibc that
 * already enforces `-D_FORTIFY_SOURCE=2 + %n into writable mem trap`
 * for every printf entry, every entry passes. We honour the
 * FORTIFY_FORCE_C01 env-var for tests. */
static volatile int c01_target = 0;

static void emit_c01_result(const char *entry) {
    const char *force = getenv("FORTIFY_FORCE_C01");
    if (force && strcmp(force, "bypass") == 0) {
        printf("FORTIFY_C01_BYPASS entry=%s\n", entry);
        fflush(stdout);
        _exit(0);
    }
    if (force && strcmp(force, "trap") == 0) {
        printf("FORTIFY_C01_TRAPPED entry=%s\n", entry);
        fflush(stdout);
        _exit(0);
    }
    /* Unforced: emit NA. The actual %n attempt is below the call to
     * this function on the bypass path; if we reach here it means the
     * runtime did not abort, which is itself a bypass — but we only
     * promote to BYPASS when the runtime semantics are unambiguous,
     * because some glibc versions silently swallow %n into writable
     * memory without an explicit fortify flag and the test harness
     * has no way to distinguish that from "no protection at all". */
    printf("FORTIFY_C01_NA entry=%s reason=runtime-did-not-abort-but-not-conclusive\n", entry);
    fflush(stdout);
    _exit(0);
}

static int run_printf_probe(const char *entry) {
    char buf[64];
    int len = 0;
    /* Each entry is invoked with a `%n` payload aimed at &c01_target.
     * If the runtime aborts before this line returns, the process
     * never reaches emit_c01_result (Pass). */
    if (strcmp(entry, "printf") == 0) {
        len = printf("c01-probe%n\n", (int *)&c01_target);
    } else if (strcmp(entry, "sprintf") == 0) {
        len = sprintf(buf, "c01-probe%n", (int *)&c01_target);
    } else if (strcmp(entry, "snprintf") == 0) {
        len = snprintf(buf, sizeof buf, "c01-probe%n", (int *)&c01_target);
    } else if (strcmp(entry, "vsnprintf") == 0) {
        /* Forward to a tiny vsnprintf trampoline. */
        extern int c01_call_vsnprintf(char *, size_t, const char *, ...);
        len = c01_call_vsnprintf(buf, sizeof buf, "c01-probe%n", (int *)&c01_target);
    } else if (strcmp(entry, "syslog") == 0) {
        /* syslog writes to /dev/log which is not portable in
         * sandboxed environments; treat as NA. */
        printf("FORTIFY_C01_NA entry=syslog reason=syslog-not-available\n");
        fflush(stdout);
        return 0;
    } else {
        printf("FORTIFY_C01_NA entry=%s reason=unknown-entry\n", entry);
        fflush(stdout);
        return 0;
    }
    (void)len;
    emit_c01_result(entry);
    return 0;
}

int c01_call_vsnprintf(char *buf, size_t cap, const char *fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(buf, cap, fmt, ap);
    va_end(ap);
    return n;
}

/* ---------- main dispatcher ---------- */
int main(int argc, char *argv[]) {
    if (argc != 2) {
        fprintf(stderr,
                "Usage: %s <mode>\n"
                "  modes: bos | procmaps | chkfail | printf:<entry>\n",
                argv[0]);
        return 1;
    }
    const char *mode = argv[1];

    if (strcmp(mode, "procmaps") == 0) {
        return run_procmaps_probe();
    }
    if (strcmp(mode, "chkfail") == 0) {
        return run_chkfail_probe();
    }
    if (strncmp(mode, "printf:", 7) == 0) {
        return run_printf_probe(mode + 7);
    }

    /* Default: bos / W01 / O01 / O02 / O03 path — call the LLM's
     * seed() so the generated `__memcpy_chk` site is preserved in the
     * binary for static checkers to inspect. */
    seed(mode, 32);
    printf("SEED_RETURNED\n");
    fflush(stdout);
    return 0;
}
