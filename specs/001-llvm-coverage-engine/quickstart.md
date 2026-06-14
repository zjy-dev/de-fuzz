# Quickstart: LLVM 覆盖率驱动 Fuzz（canary 策略）

面向开发者/研究者的最小上手路径，验证 LLVM 覆盖率后端与 GCC 等价。

## 前置条件

1. **被插桩的 Clang/LLVM**（项目外构建，类比被插桩 GCC）：
   - 用 `-fprofile-instr-generate -fcoverage-mapping` 构建。
   - 用 `-fprofile-list=<list>` 把插桩限定到栈保护相关源文件（如 `llvm/lib/CodeGen/StackProtector.cpp` 等），对齐 GCC 只插桩 `cfgexpand.o` 的白名单。
   - 产物：可运行的 instrumented `clang` 二进制 + 其源码树。
2. **LLVM 工具链命令**可用：`llvm-profdata`、`llvm-cov`、（可选）`llvm-cxxfilt`。
3. **目标源文件 IR**：对每个目标源文件离线生成 `.ll`：
   ```bash
   clang -S -emit-llvm -g -O0 llvm/lib/CodeGen/StackProtector.cpp -o build/StackProtector.ll
   ```

## 配置（示例 `configs/clang-vX.Y.Z-x64-canary.yaml`）

```yaml
compiler:
  coverage_backend: "llvm"                 # 选择 LLVM 后端（默认 gcc）
  path: "${CLANG_BUILD}/bin/clang"         # 被测 instrumented clang
  source_parent_path: "${LLVM_SOURCE}"
  llvm_profile_dir: "${CLANG_BUILD}/profraw"
  llvm_profdata_command: "llvm-profdata"
  llvm_cov_command: "llvm-cov"
  llvm_demangler_command: "llvm-cxxfilt"   # 可选
  cflags: ["-fstack-protector-strong", "-O0"]
  fuzz:
    output_root_dir: "fuzz_out"
    max_iterations: 2
    llvm_ir_paths:
      - "${CLANG_BUILD}/StackProtector.ll"
    max_constraint_retries: 2
    weight_decay_factor: 0.8
  oracle:
    type: "canary"                         # oracle/LLM 不受本特性影响
    options:
      max_buffer_size: 1024
targets:
  - file: "llvm/lib/CodeGen/StackProtector.cpp"
    functions:
      - "StackProtector::runOnFunction"
      - "StackProtector::InsertStackProtectors"
      - "StackProtector::RequiresStackProtector"
      # ... 与 GCC 配置对齐的栈保护核心函数
```

顶层 `configs/config.yaml` 的 `compiler.name`/`version`/`isa`/`strategy` 决定加载哪份编译器配置（沿用现有命名规则，前缀改 `clang`）。

## 运行

```bash
defuzz fuzz --limit 2
```

预期：引擎检测 `coverage_backend: llvm` → 装配 `LLVMCoverage` + 从 `.ll` 构造 `Analyzer` → 进入主循环：每颗 seed 编译产 profraw → merge → export JSON → 抽取被测 clang 源码已覆盖行 → 增量判定/目标选择，与 GCC 流程一致。

## 验收对照（映射到 spec）

| 检查点 | spec 引用 | 怎么看 |
|---|---|---|
| 单 seed 产非空覆盖报告 | SC-001 / FR-004 | `{state}/.../{ID}.json` 存在且 covered-lines 非空 |
| 增量判定正确 | SC-002 / FR-005 | 日志中 new-coverage true/false 与已知样本一致 |
| 续跑不归零 | SC-003 / FR-006 | 二次启动日志 "Found existing coverage data" |
| 目标 BB 合法 | SC-004 / FR-011 | 选中 BB 在目标函数内、含未覆盖行、可达 |
| GCC 无回归 | SC-005 / FR-017 | `go test ./internal/coverage/...`（GCC 测试全过） |
| 未越界 | SC-006 / FR-018 | oracle/llm/prompt 测试全过 |

## 单元测试（不依赖真实工具链）

```bash
go test ./internal/coverage/ -run 'LLVM'        # JSON 解析 / .ll→CFG / 增量 / 抽取行
go test ./internal/coverage/...                  # 含 GCC 回归
```

- fixture：`internal/coverage/artifacts/` 下放样例 `.ll` 与 `llvm-cov` JSON。
- `LLVMCoverage` 注入 fake `exec.Executor`，用预置 JSON 文件模拟 `llvm-cov export` 输出，验证 measure/merge/increase 全链路。

## 集成测试（需真实 LLVM）

`internal/coverage/llvm_integration_test.go`：若环境无 `clang`/`llvm-cov` 则 `t.Skip`，否则跑一颗真实 seed 的端到端覆盖采集。
