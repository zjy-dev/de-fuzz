_FORTIFY_SOURCE Oracle

本文档描述针对 GCC/GLIBC _FORTIFY_SOURCE 机制的跨 ISA Oracle 方案。

1. 对问题做本质抽象

与 Canary 类似，Fortify 的有效性判定也可以归纳为一种状态转换。
_FORTIFY_SOURCE 的核心在于：在运行时调用标准库函数（如 memcpy, strcpy）时，能否检测到写入长度超过了目标对象的边界。

核心机制差异

- Canary: 被动防御。溢出已经发生，但在函数返回（ret）前被拦截。
- 失败表现：Stack Smash (SIGABRT, 134)
  
  
- Fortify: 主动拦截。在数据写入发生前（或写入过程中）检测到长度非法，直接终止程序。
- 失败表现：__chk_fail -> abort() (SIGABRT, 134)
  
  
  
关键点： 为了让 Oracle 清晰地区分“Fortify 生效”与“完全被绕过”，我们需要关闭 Stack Canary。这样，如果 Fortify 失效，溢出将继续发生并覆盖返回地址，最终导致 SIGSEGV (139)。

2. 三者位置关系与单调性

我们依然利用 buf size（目标缓冲区大小）和 fill size（写入数据大小）的关系。
假设 N 为缓冲区大小（buf_size）。

状态流转（随着 fill_size 增加）

1. 正常情况 (Fortify Effective):
- fill_size <= N: 正常退出 (0)
- fill_size > N: Fortify 检测到写入越界，调用 __chk_fail。Fortify Block (SIGABRT, 134)
- 流转：0 -> 134
  
  
2. 异常情况 (Fortify Bypassed / Ineffective):
- fill_size <= N: 正常退出 (0)
- fill_size > N: Fortify 未能检测到对象大小（Object Size Unknown），执行原版 memcpy/strcpy，发生溢出。
- fill_size >> N: 溢出覆盖了 Return Address。SegFault (SIGSEGV, 139)
- 流转：0 -> 139
  
  
  
判定逻辑

这依然满足单调性。我们在 [N, Max] 区间内寻找导致崩溃的最小 fill_size。

- 如果崩溃码是 134: 说明 Fortify 捕获了溢出 -> Safe (Protected)
- 如果崩溃码是 139: 说明 Fortify 漏过了溢出，导致栈被破坏 -> Vulnerable (Bypassed)
  
3. 技术实现细节

3.1 隔离干扰

必须使用 -fno-stack-protector 编译，确保 134 (SIGABRT) 只能由 Fortify (__chk_fail) 产生，而不是 Canary。

3.2 编译器优化

_FORTIFY_SOURCE=2 必须配合 -O2 (或 -O1) 使用。如果使用 -O0，Fortify 将不会生效，这会导致误报（认为不安全，实际是因为没开优化）。

3.3 欺骗编译器 (Trick the Compiler)

Fortify 在编译时也会进行检查。如果编译器在编译期就能确定 fill_size > buf_size，它会直接报错停止编译，或者生成直接报错的汇编。
为了测试运行时的 Fortify 机制，我们需要让 fill_size 对编译器来说是“未知的”（runtime known），这在你的模板中通过 atoi(argv) 已经完美实现。

4. 函数模板设计

以下是适配 Fortify Source 的模板。相比 Canary 模板，它强制使用标准库函数（memcpy/strcpy），因为 Fortify 不保护手写的 for 循环赋值。

```c
/**
 * Fortify Source Oracle Function Template
 *
 * Purpose: Test _FORTIFY_SOURCE effectiveness across different scenarios.
 * Strategy: Disable Stack Canary to isolate Fortify's behavior.
 *
 * Usage: ./prog <buf_size> <fill_size>
 * Compile: gcc -O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector program.c -o program
 *
 * Expected behavior:
 * - Normal: exit 0
 * - Fortify Caught: exit 134 (SIGABRT via __chk_fail)
 * - Fortify Bypassed: exit 139 (SIGSEGV via ret addr overwrite)
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <alloca.h>
#include <unistd.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 *
 * LLM Instructions:
 * - Implement void seed(int buf_size, int fill_size)
 * - MUST use a libc function vulnerable to overflow (memcpy, strcpy, sprintf, etc.)
 * - DO NOT use manual loops for copying (Fortify won't catch those).
 * - Construct the buffer based on buf_size.
 * - Perform the overflow based on fill_size.
 * - MUST print "SEED_RETURNED" and fflush before returning (sentinel for false positive detection)
 *
 * Common Test Patterns:
 *
 * 1. Standard Stack Buffer (memcpy):
 *    void seed(int buf_size, int fill_size) {
 *        char buffer[64];
 *        char source[4096];
 *        memset(source, 'A', sizeof(source));
 *        memcpy(buffer, source, fill_size);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 *
 * 2. Structure Member Overflow (checking sub-object protection):
 *    struct wrapper { char buf[64]; int x; };
 *    void seed(int buf_size, int fill_size) {
 *        struct wrapper w;
 *        char source[4096];
 *        memset(source, 'A', fill_size);
 *        source[fill_size > 0 ? fill_size - 1 : 0] = '\0';
 *        strcpy(w.buf, source);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 *
 * 3. Dynamic Stack Buffer (VLA/alloca) - Fortify often fails here!
 *    void seed(int buf_size, int fill_size) {
 *        char *buffer = alloca(buf_size);
 *        char source[4096];
 *        memset(source, 'A', sizeof(source));
 *        memcpy(buffer, source, fill_size);
 *        printf("SEED_RETURNED\n");
 *        fflush(stdout);
 *    }
 */

// Disable stack protector for main to maximize attack surface
// This ensures SIGABRT only comes from Fortify, not Canary
#define NO_CANARY __attribute__((no_stack_protector))

void seed(int buf_size, int fill_size); // Forward declaration

NO_CANARY int main(int argc, char *argv[]) {
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
```

5. 预言机算法逻辑 (Python 伪代码)

def check(buf_size, fill_size):
    # 编译命令必须包含 -O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector
    exit_code = run_program(buf_size, fill_size)
    return exit_code

def fortify_oracle(buf_size):
    L = 0
    R = MAX_FILL_SIZE # e.g., 4096
    crash_detected = False
    min_crash_size = -1
    crash_code = 0

    # 1. 二分查找最小崩溃点
    while L <= R:
        mid = (L + R) // 2
        code = check(buf_size, mid)
        if code != 0:
            min_crash_size = mid
            crash_code = code
            R = mid - 1
            crash_detected = True
        else:
            L = mid + 1

    # 2. 判定逻辑
    if not crash_detected:
        return "UNKNOWN (No Crash Found)"
    
    if crash_code == 134:
        # SIGABRT: __chk_fail triggered
        return "SAFE (Fortify Protected)"
    
    if crash_code == 139:
        # SIGSEGV: Return Address corrupted
        return "VULNERABLE (Fortify Bypassed)"
    
    return f"UNKNOWN (Exit Code {crash_code})"
```

## 6. 假阳性处理：哨兵输出机制

### 问题

与 Canary Oracle 相同，SIGSEGV 可能由两种情况触发：
1. **真正的 Fortify bypass**：返回地址被覆盖，函数返回时崩溃
2. **间接崩溃（假阳性）**：局部变量被溢出覆盖，导致函数内部崩溃

例如：溢出破坏了 `fill_size` 参数的栈副本，后续的 `memcpy` 使用错误值导致 SIGSEGV，这是假阳性。

### 解决方案：哨兵输出 (Sentinel Output)

在 `seed()` 函数内部、**return 语句之前**添加哨兵标记：

```c
void seed(int buf_size, int fill_size) {
    char buffer[64];
    char source[4096];
    memset(source, 'A', sizeof(source));
    memcpy(buffer, source, fill_size);
    
    // Sentinel: must be INSIDE seed(), before return
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
```

### 判定逻辑

| stdout 包含 `SEED_RETURNED` | 退出码 | 判定 |
|---------------------------|--------|------|
| Yes | SIGSEGV (139) | **真正的 Fortify bypass** - 报告 Bug |
| No | SIGSEGV (139) | **间接崩溃** - 不报告 (假阳性) |
| - | SIGABRT (134) | **Fortify 正常工作** - 安全 |

### 原理

- **有哨兵 + SIGSEGV**：`seed()` 正常返回后崩溃，说明返回地址被覆盖但 Fortify 没检测到
- **无哨兵 + SIGSEGV**：`seed()` 内部崩溃，可能是局部变量被破坏导致的间接溢出

## 7. 注意事项

### 7.1 VLA/alloca 导致 Fortify 失效是预期行为

Fortify 的核心依赖 `__builtin_object_size(buf, 1)` 在编译时确定对象大小。以下场景会使其返回 `-1`（未知大小），导致 Fortify 失效：
- VLA（变长数组）
- alloca()
- 复杂指针别名
- 外部函数返回的指针

这是 Fortify 机制本身的限制，不是编译器 bug。Oracle 应该能检测出这种情况（报告为 Vulnerable），但需要在报告中明确说明这是"设计限制"而非"实现缺陷"。

### 7.2 编译参数要求

必须使用以下编译参数：
```bash
gcc -O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector program.c -o program
```

- `-O2`：Fortify 需要优化才能生效
- `-D_FORTIFY_SOURCE=2`：启用完整的 Fortify 检查
- `-fno-stack-protector`：禁用 Canary，隔离干扰
