// Test seed 1: Stack overflow attempt
int main() {
  char buffer[8];
  char *src = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
  int i = 0;
  while (src[i]) {
    buffer[i] = src[i];
    i++;
  }
  return 0;
}
