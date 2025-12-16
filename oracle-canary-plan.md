# stack canary oracle

本文档描述我原创的针对 stack canary 的跨 isa 的 oracle 方案.

## 对问题做本质抽象

stack canary 是否生效, 其实就是看以下两个东西:

- `canary` 是否存在, 如果不存在自然是失效了
- 如果存在, 那就是看 `canary` & `return address` & `buffer 起始地址` 的位置关系.

我设计的预言机的关键就是渐进或二分的增加 buffer 中填充物的 size, 以字节为单位增加, 简称 `buf size`. 设定一个 `buf size` 的阈值为 `N`, 当 `size >= N` 时就认为 size 已经足够大到能够覆盖 caller 的 ret addr.

## 三者位置关系

在所有主流 ISA 中, 栈地址都是从高往低生长的, 即 caller 的 stack frame 在高地址, callee 在低地址.

以下以 -> 代表从高到低, 以 ret 代表 return address, buf 代表 buffer start address.

> 正常情况: 程序正常退出(简称`正常退出`), canary check fail(简称 `canary_chk_fail`, 返回值是)

> 异常情况: canary 没有触发, ret address 却被修改了(简称 `ret_modified`)

以下小标题中的位置关系是编译器实际生成的位置关系. 我们的目的是能测出编译器没有按要求工作的情况.

### 1. ret -> canary -> buf

一种安全的情况. 随着 buf size 的增加程序的退出状态: `正常退出 -> canary_chk_fail`

### 2. ret -> buf -> canary

不安全的情况. 手动保证 caller 中不存在 canary(恶意最大化), 那么此时随着 buf size 的增加: `正常退出 -> ret_modified`

### 3. canary -> ret -> buf

不安全, [cve-2023-4039](https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html) 中的情况.

随着 buf size 的增加([0, N]): `正常退出 -> ret_modified -> canary_chk_fail`

### 4. canary -> buf -> ret

安全, arm 的常规情况. canary 保护 caller 的栈帧.

随着 buf size 的增加: `正常退出 -> canary_chk_fail`.

### 5. buf -> ret -> canary

一种极其不安全的情况. 目前没见过这种情况. 手动保证 caller 中不存在 canary(恶意最大化), 那么随着 buf size 的增加: `正常退出 -> ret_modified`.

### 6. buf -> canary -> ret

一种极其不安全的情况. 目前没见过这种情况. 手动保证 caller 中不存在 canary(恶意最大化), 那么随着 buf size 的增加: `正常退出 -> ret_modified`.

## 二分预言机设计

在以上所有情况的讨论中, 都存在一种 `正常退出 -> ret_modified -> canary_chk_fail` 的单调性! 只是有些情况会缺少 `ret_modified`, 有些情况会缺少 `canary_chk_fail` 而已.

因此二分的理论基础是成立的.

### 具体实现

在 [0, N] 之间二分查找 `ret_modified` 的情况即可.

算法如下：

1. 定义 `check(size)`: 使用长度为 `size` 的输入运行程序，返回退出代码。
2. 在 `[0, N]` 区间内二分查找**最小的**导致程序崩溃（非 0 退出码）的输入长度 `min_crash_size`。
   - 初始化 `L = 0`, `R = N`, `ans = -1`
   - 当 `L <= R`:
     - `mid = (L + R) / 2`
     - `exit_code = check(mid)`
     - 如果 `exit_code != 0`: 记录 `ans = mid`, 尝试更小的长度 `R = mid - 1`
     - 如果 `exit_code == 0`: 尝试更大的长度 `L = mid + 1`
3. 结果判定:
   - 如果 `ans == -1`: 未发现崩溃，返回 Safe (可能 N 太小或无漏洞)。
   - 获取 `crash_code = check(ans)`。
   - 如果 `crash_code == 139` (SIGSEGV): **BUG FOUND** (Ret 被修改且 Canary 未拦截)。
   - 如果 `crash_code == 134` (SIGABRT): **Safe** (Canary 成功拦截)。
   - 其他退出码: 需进一步分析，暂定为 Unknown 或 Safe。

## 技术实现

在以上的方案讨论中, 我们必须能通过程序实现一些操作.

### 1. `ret_modified` & `canary_chk_fail` 的判定

在 linux/mac 上, `canary_chk_fail` 时, `stack_chk_fail()` 函数往往会调用 `abort()`, 造成程序返回值为 `134 (128 + 6)`

而在 `ret_modified` 时, 如果跳转到非法地址(容易构造), 往往程序会返回 `139 (128 + 11)`

因此可以通过监测程序的返回值来做到这一点.

### 2. caller 最大恶意

即保证 caller 中不包含 canary, 可以通过函数模板 + attribute 来实现, 示例代码如下:

```c
#define NO_CANARY __attribute__((no_stack_protector))

NO_CANARY int main() {
    // call seed function
    seed();

    return 0;
}
```

### 3. `N` 的取值

`N` 需要足够大以覆盖当前栈帧并触及返回地址。

- 对于大多数简单的测试用例，局部变量缓冲区通常较小
- 默认值设为 **4096 (4KB)** 通常足够
- 可通过配置项 `max_buffer_size` 进行调整

### 4. 跨 ISA 的非法返回地址构造

为了确保 `ret_modified` 能够稳定触发 SIGSEGV (139) 而不是意外跳转到有效地址，填充数据应构造为非法地址

- 使用字符 `'A' (0x41)` 进行填充。
- 在 64 位系统 (x64, AArch64) 上，`0x4141414141414141` 是一个非规范地址 (Non-canonical address) 或未映射地址，访问该地址会导致段错误

### 5. 函数模板

目前生成了一个 work 的模板, 如下:

```c
/**
 * Canary Oracle Function Template
 *
 * This template is used for testing stack canary protection mechanisms.
 * The LLM generates only the seed() function body.
 *
 * Usage: ./prog <buffer_size>
 *   - buffer_size: Number of 'A' characters to write into the buffer
 *   - Example: ./prog 100 writes 100 'A's into the buffer
 *
 * Expected behavior:
 *   - Small sizes: Program exits normally (return 0)
 *   - Medium sizes (canary overwritten): SIGABRT (exit code 134)
 *   - Large sizes (ret addr overwritten without canary): SIGSEGV (exit code
 * 139)
 *
 * The canary oracle uses binary search on buffer_size to detect
 * vulnerabilities.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement a function that contains a local buffer (char array)
 * - The function receives 'fill_size' parameter indicating how many bytes to
 * fill
 * - Use memset or a loop to fill the buffer with 'A' (0x41) characters
 * - The buffer should be vulnerable to overflow (no bounds checking)
 * - DO NOT add any stack protection attributes to this function
 * - Example signature: void seed(int fill_size)
 *
 * Example implementation patterns:
 * 1. Simple buffer overflow:
 *    void seed(int fill_size) {
 *        char buffer[64];
 *        memset(buffer, 'A', fill_size);
 *    }
 *
 * 2. With pointer arithmetic:
 *    void seed(int fill_size) {
 *        char buf[32];
 *        char *p = buf;
 *        for (int i = 0; i < fill_size; i++) *p++ = 'A';
 *    }
 */

// Disable stack protector for main to maximize attack surface
// This ensures canary check only happens in seed() if at all
#define NO_CANARY __attribute__((no_stack_protector))

NO_CANARY int main(int argc, char *argv[]) {
  if (argc != 2) {
    fprintf(stderr, "Usage: %s <buffer_size>\n", argv[0]);
    return 1;
  }

  int fill_size = atoi(argv[1]);
  if (fill_size < 0) {
    fprintf(stderr, "Error: buffer_size must be non-negative\n");
    return 1;
  }

  // Call the seed function with the specified fill size
  seed(fill_size);

  return 0;
}
```
