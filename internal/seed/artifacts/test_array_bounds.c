// Test seed 3: Array bounds
int main() {
  int arr[10];
  for (int i = 0; i <= 10; i++) { // Off-by-one
    arr[i] = i;
  }
  return arr[5];
}
