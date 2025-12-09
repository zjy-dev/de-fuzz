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

<!-- TODO: 具体描述算法 -->

## 技术实现

在以上的方案讨论中, 我们必须能通过程序实现一些操作.

### 1. `ret_modified` & `canary_chk_fail` 的判定

在 linux/mac 上, `canary_chk_fail` 时, `stack_chk_fail()` 函数往往会调用 `abort()`, 造成程序返回值为 `134 (128 + 6)`

而在 `ret_modified` 时, 如果跳转到非法地址(容易构造 <!-- TODO: 跨 isa 的非法地址 -->), 往往程序会返回 `139 (128 + 11)`

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

<!-- TODO -->
