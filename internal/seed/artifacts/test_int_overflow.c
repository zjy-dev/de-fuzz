// Test seed 2: Integer overflow
int main() {
  int x = 2147483647; // INT_MAX
  int y = x + 1;
  return y < 0 ? 1 : 0;
}
