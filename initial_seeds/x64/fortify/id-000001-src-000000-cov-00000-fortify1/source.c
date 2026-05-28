/**
 * FORTIFY Oracle - sample seed #1: BOS-eligible struct-last-member memcpy
 *
 * This seed exercises INV-FORT-W01 / O01 by routing a memcpy() through a
 * struct's last char[] member. With `_FORTIFY_SOURCE=2 -O2` the compiler
 * is required to emit a `__memcpy_chk` call site whose dstlen reflects
 * BOS(s.tail). The static checkers inspect that exact call site.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct fortify_last_member {
    int header;
    char tail[16];
};

void seed(const char *mode, int n) {
    struct fortify_last_member s;
    s.header = 0xdeadbeef;
    /* Pass dst as an opaque char* so the compiler keeps BOS context
     * but cannot fold the call away at -O2. */
    char *dst = s.tail;
    memcpy(dst, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", (size_t)n);
    (void)mode;
    (void)s.header;
}

int main(int argc, char *argv[]) {
    (void)argc;
    seed(argv[1], 8);
    printf("SEED_RETURNED\n");
    fflush(stdout);
    return 0;
}
