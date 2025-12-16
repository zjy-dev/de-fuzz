// Test seed 4: Complex control flow
int fibonacci(int n) {
  if (n <= 1)
    return n;
  return fibonacci(n - 1) + fibonacci(n - 2);
}

int factorial(int n) {
  if (n <= 1)
    return 1;
  return n * factorial(n - 1);
}

int main() {
  int a = fibonacci(10);
  int b = factorial(5);

  if (a > b) {
    return a - b;
  } else if (a < b) {
    return b - a;
  }
  return 0;
}
