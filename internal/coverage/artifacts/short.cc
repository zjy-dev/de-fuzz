#include <bits/stdc++.h>
using namespace std;

int log_level = 2;

void f(bool flag, int a, int b) {

  if (log_level == 1) {
    cout << "log level is" << log_level << endl;

    if (flag)
      cout << "flag is true" << endl;
  } else if (log_level == 2) {
    cout << "log level is" << log_level << endl;

    cout << log_level << endl;

    if (flag)
      cout << "flag is true" << endl;
    else
      cout << "flag is false" << endl;
  } else
    cout << "log is off";

  if (flag) {
    // ...
    if (a > 0)
      cout << "a > 0" << endl;
    else if (a == 0) {
      cout << "a == 0" << endl;
    }
    //  ...
  } else if (!flag) {
    switch (b) {
    case 0:
      cout << "b == 0" << endl;
      break;
    case 1:
    case 2:
      cout << "Uncovered" << " ";
      cout << "Lines1" << " ";
    default:
      cout << "default" << endl;
      break;
    }
  } else {
    cout << "Uncovered" << " ";
    cout << "Lines2" << " ";
  }
}