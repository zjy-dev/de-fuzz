#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>
#include <unistd.h>

void seed(int buf_size, int fill_size) {
    char small[8];
    char exact[16];
    char *pdst = small;
    volatile size_t n = (size_t)(fill_size > 0 ? fill_size : 1);
    char source[4096];
    memset(source, 'A', sizeof(source));

    // Compile-time-known overflow candidate for FORTIFY warning/check folding.
    memcpy(small, source, 16);

    // Compile-time-known safe copy for contrast.
    memcpy(exact, source, sizeof(exact));

    // Runtime-dependent copy to keep dynamic behavior in play.
    memcpy(pdst, source, n);
    putchar(pdst[0]);

    // Self-copy/no-op style path for memmove-family simplification.
    memmove(exact, exact, buf_size > 0 ? 1 : 0);

    printf("memory family: fill=%d buf=%d\n", fill_size, buf_size);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}

#define NO_CANARY __attribute__((no_stack_protector))

NO_CANARY int main(int argc, char *argv[]) {
    if (argc != 3) {
        fprintf(stderr, "Usage: %s <buf_size> <fill_size>\n", argv[0]);
        return 1;
    }

    int buf_size = atoi(argv[1]);
    int fill_size = atoi(argv[2]);
    if (buf_size < 0 || fill_size < 0) {
        return 1;
    }

    seed(buf_size, fill_size);
    return 0;
}
