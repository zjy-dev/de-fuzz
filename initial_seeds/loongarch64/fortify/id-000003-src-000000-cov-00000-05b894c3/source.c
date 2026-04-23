#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>
#include <unistd.h>

void seed(int buf_size, int fill_size) {
    char *buffer = alloca(buf_size > 0 ? buf_size : 1);
    char fixed[8];
    char source[4096];
    int pipefd[2];
    memset(source, 'C', sizeof(source));

    sprintf(fixed, "%s", "ABCDEFGHIJK");
    snprintf(fixed, sizeof(fixed), "X=%d:%s", fill_size, source);
    snprintf(buffer, buf_size > 0 ? buf_size : 1, "%s", source);

    if (pipe(pipefd) == 0) {
        int write_size = fill_size > 0 ? fill_size : 1;
        if (write_size > 64) {
            write_size = 64;
        }
        write(pipefd[1], source, write_size);
        close(pipefd[1]);
        read(pipefd[0], fixed, buf_size > 0 ? buf_size : 1);
        close(pipefd[0]);
    }

    if (getcwd(fixed, buf_size > 0 ? buf_size : 1) == NULL) {
        fixed[0] = '\0';
    }

    FILE *fp = tmpfile();
    if (fp != NULL) {
        fputs(source, fp);
        rewind(fp);
        fgets(fixed, buf_size > 0 ? buf_size : 1, fp);
        fclose(fp);
    }

    printf("format/io family: buf_size=%d fill_size=%d\n", buf_size, fill_size);
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
