#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>
#include <unistd.h>

struct wrapper {
    char buf[64];
    int marker;
};

void seed(int buf_size, int fill_size) {
    struct wrapper w;
    char source[4096];
    memset(source, 'B', sizeof(source));
    strcpy(w.buf, "ABCDEFGHIJK");

    if (fill_size >= (int)sizeof(source)) {
        fill_size = (int)sizeof(source) - 1;
    }
    source[fill_size > 0 ? fill_size - 1 : 0] = '\0';

    w.buf[0] = '\0';
    strcpy(w.buf, source);
    strncpy(w.buf, source, buf_size > 0 ? buf_size : 1);
    strncat(w.buf, source, buf_size > 0 ? buf_size : 1);

    printf("string family: fill=%d buf=%d marker=%d\n", fill_size, buf_size, w.marker);
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
