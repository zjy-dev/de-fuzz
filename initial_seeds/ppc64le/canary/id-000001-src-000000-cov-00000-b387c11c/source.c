#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void seed(int buf_size, int fill_size) {
    (void)buf_size;
    char buffer[64];
    memset(buffer, 'A', fill_size);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}

int main(int argc, char *argv[]) {
    if (argc != 3) {
        fprintf(stderr, "Usage: %s <buf_size> <fill_size>\n", argv[0]);
        return 1;
    }

    int buf_size = atoi(argv[1]);
    int fill_size = atoi(argv[2]);
    if (buf_size < 0 || fill_size < 0) {
        fprintf(stderr, "Error: sizes must be non-negative\n");
        return 1;
    }

    seed(buf_size, fill_size);
    return 0;
}
