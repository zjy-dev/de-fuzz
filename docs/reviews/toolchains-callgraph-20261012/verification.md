# LLVM 缺失与 CallGraph 版本错配修复验收

日期：2026-10-12。环境：Apple Silicon macOS，rootless Podman ARM64 VM，
Linux amd64 工具链/应用镜像；目标程序经原生 x86 转译或显式 QEMU AArch64 执行。
本次没有模型调用，也没有启动正式编译器研究 campaign。

## 结论

两项问题均已复现并修复，默认 Pod 已更新并保持运行。

| 项目 | 修复前 | 修复后 |
| --- | --- | --- |
| LLVM | 原 `pi-final` 镜像找不到 clang、llvm-config | core 和 orchestrator 均包含 Clang/LLVM/LLD 23.1.2 |
| GCC 与图版本 | 容器 GCC17，内置图 GCC16.2.0 | 默认 GCC16.2.0 x86_64/AArch64，使用对应发布源码 |
| LLVM 与图版本 | 缺少可执行 LLVM，图标为 23.1.2 | 官方 LLVM23.1.2 二进制，其版本输出携带发布提交 |
| AArch64 Clang | 通用宿主 Clang 会默认生成 x86_64 | 专用驱动明确选择 aarch64-linux-gnu，采用 LLD |
| 版本漂移检查 | 图名字可由代理自行选择 | 调用前检查源码版本、字节锚点、ISA 和驱动版本；记录数据库哈希，并限制 MCP target |

GCC17 四 ISA 的旧工具链与配置保留，用于历史实验重放。不能用 GCC16 图冒充
GCC17；该配置下 Pi mapping/audit 会拒绝运行，需另外生成对应图。
没有修改已有正式 campaign 的版本或预算。

## 工具链和数据身份

- GCC16.2.0 发布提交：`78d4ac73dd391005b895a6148cd9831e28e1208b`。
- LLVM23.1.2 发布提交：`85ac560262434c9ccfc0c183ec22d4138ed647fb`。
- 下载包哈希：`configs/toolchains.matched.lock.json`。
- 匹配工具链镜像：`693d9c9094719f16a87575f1415a18c9efd0563f172358232575e850fae6b2f7`。
- core：`2360fb60ec0142fb6ad695ed2fe28e79be0b002f59720a850b123585880b3aee`。
- orchestrator（`dev` 和更新后的 `pi-final`）：
  `bddef890b9ab25e14131cae271432094b7b5fa3528ac3f234883fc343d3e297f`。
- SQLite：`18cb0e494f916388f7af98e75a0fa3931eed0b6c95016d83cfec5f4d86aae475`。
- 图边数未改变：GCC x86_64 579,916；GCC AArch64 582,555；
  LLVM 2,025,915；合计 3,188,386。
- 源码字节锚点：GCC x86_64 1,130 个；AArch64 1,145 个；LLVM 4,234 个，
  全部与发布源码匹配。

原始 CodeQL 归档的构建日志标识这些发布版本，但没有提取时的 checkout commit。
记录中的发布提交来自官方标签与原始日志，不能据此声称独立证明了历史提取
环境的全部源码字节。`revision_evidence` 明确保留该限制。
LLVM 构建日志仅确认 X86/AArch64；CSV 中出现其他后端符号不能证明对应 ISA
已参与构建。静态图也不包含全部函数指针调用。

容器源码目录为 `/opt/defuzz-sources/gcc-16.2.0` 和
`/opt/defuzz-sources/llvm-project-23.1.2`。对应发布包 SHA-256、安装文件哈希、
基础镜像 ID 存于 `/usr/local/share/defuzz/toolchains-matched.json`。

## 验证结果

| 检查 | 结果与证据 |
| --- | --- |
| GCC/LLVM × 两个 ISA | 4/4 真实 C 编译、ELF machine 检查和执行通过，exit 0；`container-toolchain-smoke.json` |
| LLVM LTO/CFI | x86_64/AArch64 的正常测试程序链接与执行通过；官方 LLD 和 ICU70 依赖可用 |
| 真实 MCP | 六工具可列举；匹配 target 可查函数；其他版本/编译器 target 返回错误 |
| 源码/图绑定 | 两发布源码的全部已登记锚点匹配；`source-bindings.json` |
| 默认 Pod | 两轮 × 两 ISA 的 gRPC 构建、INV-SP-G01 判定、运行、归档均通过；`default-pod-smoke.txt` |
| 存储/回收 | 共享 2 GiB tmpfs、只读调用图卷、成功归档后清理、失败构建清理、无关 sentinel 保留，均通过 |
| Python 回归 | 679 项通过；此后阶段相关 109 项、入口 105 项、SDK/身份 31 项和旧映射入口 8 项复验通过 |
| CallGraph 回归 | 3 项通过；真实 MCP allowlist 验证覆盖额外行为 |
| Ruff / Mypy | 改动路径通过；`ruff.log`、`mypy.log` |

构建过程中曾尝试混用 GCC 配置下的对象与 Clang 对象，产生启动/编译崩溃。
弃用该组产物，重新一致构建 compiler 对象后才通过真实编译执行。AArch64
compiler 对象使用 Clang14 `-g -O1 -fno-strict-aliasing`；x86 使用 `-g -O2`；
源码不变。target libgcc 由新 GCC 在 amd64 runtime 中构建。
`gcc-build-method.json` 保留配置和实际构建方式。构建均未启用 GCC 内部
self-tests，日志中的“self-tests are not enabled”只证明编译器启动完成，
不能视为测试套件通过；验收依据是另行执行的编译/链接/运行检查。

默认配置已切到 `configs/toolchains.matched.container.yaml`。原 Pod 停止后，
备份 `.work/podman/pod.pre-toolchain-match.yaml`，再更新默认 Pod。旧 `pi-final`
另存为 `pi-final-pre-toolchains-20261012`。独立验收 Pod 已删除，产物保留。

## 剩余限制

本机 amd64 QEMU 容器中的 bubblewrap 用户命名空间探针仍失败：

```text
bwrap: Creating new namespace failed, likely because the kernel does not support user namespaces.  bwrap must be installed setuid on such systems.
```

因此本次修复不代表正式 Pi campaign 可以在该模拟环境启动。正式隔离门禁未降低，
未请求模型，也未记录编译器 defect verdict。正式实验仍需可用的隔离宿主、
clean Git 输入、冻结配置、完整适用输入及既定预算；选择两个 ISA 的匹配图
不会自动满足四 ISA 比较或其他差分 checker 依赖。

本次实现修复、工具链构建和工程 smoke 的完成与正式全量实验的完成分开记录。
