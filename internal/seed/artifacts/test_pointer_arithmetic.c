// Test seed 5: Pointer arithmetic
int main() {
  int arr[10] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
  int *ptr = arr;

  // Safe pointer arithmetic
  for (int i = 0; i < 10; i++) {
    *(ptr + i) = i * 2;
  }

  // Potentially unsafe - but still within bounds
  int *end = ptr + 9;
  *end = 100;

  return *ptr + *end;
}
