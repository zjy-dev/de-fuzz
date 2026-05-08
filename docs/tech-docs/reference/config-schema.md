---
title: Configuration Schema Reference
description: configs/*.yaml 全字段对照表，含顶层 config + compiler + fuzz + oracle + flag_strategy + targets
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../guides/cflags-configuration.md
  - ../features/flag-scheduler.md
  - ../features/mechanism-contract.md
---

# Configuration Schema Reference

DeFuzz 用两层 YAML：

- **顶层** `configs/config.yaml` —— 全局：strategy / ISA / log / remixer 路径 / compiler name+version。
- **目标编译器配置** `configs/gcc-vX.Y.Z-<isa>-<strategy>.yaml` —— 具体的 compiler 路径 + fuzz + oracle + targets。

`internal/config/config.GetCompilerConfigPath` 用顶层的 `compiler.name + version`、`isa`、`strategy` 拼出第二份的文件名（例如 `gcc-v15.2.0-aarch64-canary.yaml`）。两份文件都被 `LoadConfig()` 合并成一个 `Config`。

## 1. 顶层 config.yaml

```yaml
config:                                  # 必须有这个外层 key
  remixer_config: "configs/remixer.yaml" # LLM provider 路由配置（OpenAI / Anthropic / Remixer）
  default_temperature: 0.1               # 全局默认；可被 prompt 阶段覆盖
  isa: "aarch64"                         # 决定 function_template 路径 + qemu / native
  strategy: "canary"                     # 决定 mechanism contract、initial_seeds 子目录
  log_level: "info"                      # debug / info / warn / error / fatal
  log_dir: "./logs"                      # 空 = 仅 console；非空 = 时间戳文件
  compiler:
    name: "gcc"                          # 决定第二份配置文件名前缀
    version: "15.2.0"
```

**字段映射**：见 `internal/config/config.go` `Config` 结构（`mapstructure` tag）。

## 2. 编译器配置：compiler 顶层

```yaml
compiler:
  path: "/path/to/xgcc"                  # 唯一必填
  gcovr_exec_path: "/path/to/build"      # gcovr 执行目录
  source_parent_path: "/path/to/source"  # gcovr -r 锚点
  gcovr_command: 'gcovr ... -r ..'       # 完整命令；不允许末尾的 --json 输出参数
  cflags:                                # baseline cflags；profile / LLM cflags 后追加
    - "-fstack-protector-strong"
    - "-O0"
    - "--sysroot=/..."
    - "-B/..."
    - "-L/..."
  total_report_path: ""                  # 可选；空 = 默认 {output}/state/total.json
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `path` | ✅ | 不存在 → fuzzer 启动期失败 |
| `gcovr_exec_path` | ✅ | gcovr 在此目录运行 |
| `source_parent_path` | ✅ | 用于 coverage 报告路径解析 |
| `gcovr_command` | ✅ | 模板字符串；最后会拼上 `--json output.json` |
| `cflags` | ⚠ 可选 | 缺省时 fuzzer 会使用 `["-fstack-protector-strong","-O0"]` 并 warn |
| `total_report_path` | ⚠ 可选 | 想用集中式中央报告时再指定 |

详见 `@/home/yall/project/de-fuzz/docs/tech-docs/guides/cflags-configuration.md`。

## 3. compiler.fuzz

```yaml
compiler:
  fuzz:
    output_root_dir: "fuzz_out"
    max_iterations: 256
    max_new_seeds: 1
    max_test_cases: 0                    # 0 = 不生成 test_cases 段
    function_template: ""                # ⚠ 已废弃：被 mechanism contract 取代
    base_prompt_dir: "prompts/base"
    timeout: 30
    use_qemu: true
    qemu_path: "qemu-aarch64"
    qemu_sysroot: "/path/to/sysroot"
    cfg_file_path: "/path/to/cfgexpand.cc.015t.cfg"   # 单 CFG (向后兼容)
    cfg_file_paths:                                     # 多 CFG (推荐)
      - "/path/to/cfgexpand.cc.015t.cfg"
      - "/path/to/function.cc.015t.cfg"
    mapping_path: ""                     # 空 = {output}/state/coverage_mapping.json
    max_constraint_retries: 8
    weight_decay_factor: 0.8             # (0, 1]
    flag_strategy: { ... }               # 见 §5
```

**字段映射**：`internal/config/config.go` `FuzzConfig`。CLI flag 覆盖优先级：`--output > output_root_dir`、`--limit > max_iterations`、`--timeout > timeout`、`--use-qemu > use_qemu`、`--log-dir > log_dir`。

**已废弃字段**：`function_template`。从 commit `a7307b6` 起，该路径由 `mechanism.Contract.FunctionTemplatePath(cfg.ISA)` 推导；YAML 里写它会被忽略。

## 4. compiler.oracle

```yaml
compiler:
  oracle:
    type: "canary"                       # 必须 = mechanism.Contract.OracleType()
    options:
      max_buffer_size: 1024              # DynamicBufferSearchChecker 二分上限
      default_buf_size: 64               # seed() 第一个参数默认值
      negative_cflags:                   # CanaryOracle.polarityFor 的 invert 触发条件
        - "-fno-stack-protector"
```

`options` 结构是 `map[string]interface{}`，每个 oracle 自己解；canary oracle 的字段定义在 `internal/oracle/canary_oracle.go:NewCanaryOracle`。

## 5. compiler.fuzz.flag_strategy

```yaml
compiler:
  fuzz:
    flag_strategy:
      enabled: true
      mode: "matrix"                     # 唯一支持的取值
      allow_llm_cflags: false
      include_negative_controls: true
      selection_order: "deterministic"   # LoadConfig 默认填这个
      negative_controls:
        - ["-fno-stack-protector"]
      axes:
        common:
          policy:
            - ["-fstack-protector"]
            - ["-fstack-protector-strong"]
            - ["-fstack-protector-all"]
            - ["-fstack-protector-explicit"]
          threshold:
            - ["--param=ssp-buffer-size=1"]
            - ["--param=ssp-buffer-size=8"]
            - ["--param=ssp-buffer-size=32"]
          pic_mode:
            - []
            - ["-fPIC"]
        by_isa:
          aarch64:
            guard_source:
              - []
              - ["-mstack-protector-guard=global"]
              - ["-mstack-protector-guard=sysreg", "-mstack-protector-guard-reg=x18", "-mstack-protector-guard-offset=0"]
      isa_options:
        aarch64:
          stack_protector_guard_reg: "x18"
          supports_hardware_tls: false
```

完整语义：`@/home/yall/project/de-fuzz/docs/tech-docs/features/flag-scheduler.md`。**当前限制**：`buildProfilesForISA` 只接受 `isa == "aarch64"`，其它 ISA 启用 `flag_strategy.enabled=true` 会失败。

## 6. targets

```yaml
targets:
  - file: "gcc/gcc/cfgexpand.cc"
    functions:
      - "stack_protect_classify_type"
      - "expand_used_vars"
  - file: "gcc/gcc/function.cc"
    functions:
      - "stack_protect_epilogue"
```

`file` 路径必须**与 gcovr JSON 报告里的 `file` 字段完全一致**（gcovr 的路径取决于 `gcovr_command` 中的 `-r`）；最常见的不匹配源于 `gcovr -r ..` vs 实际 source 路径不对应。

`functions` 列表用于 `Analyzer.GetTotalTargetLines` 计算"target lines"；只有在 `cfg_file_paths` 里至少有一份 dump 包含这些函数时才会被计入。

## 7. 环境变量替换

YAML 中字符串值支持 `${VAR}` / `$VAR` 写法（`internal/config/config.go:resolveEnvVars`）：

```yaml
compiler:
  path: "${GCC_BUILD}/gcc/xgcc"
  fuzz:
    qemu_sysroot: "${GCC_INSTALL}/aarch64-none-linux-gnu/libc"
```

变量来源（按优先级）：

1. 进程环境（`os.Environ`）；
2. 项目 `.env` 文件 (`config.LoadEnvFromDotEnvRecursive`)，向上递归 5 层 + 工作目录 10 层。

`.env` 不存在不报错；变量未设置则保留原占位（不报错），调用方负责检查路径有效性。

## 8. minimal canary 配置（速参）

```yaml
# configs/config.yaml
config:
  remixer_config: "configs/remixer.yaml"
  default_temperature: 0.1
  isa: "aarch64"
  strategy: "canary"
  log_level: "info"
  log_dir: "./logs"
  compiler:
    name: "gcc"
    version: "15.2.0"

# configs/gcc-v15.2.0-aarch64-canary.yaml
compiler:
  path: "${GCC_BUILD}/gcc/xgcc"
  gcovr_exec_path: "${GCC_BUILD}"
  source_parent_path: "${GCC_SOURCE_PARENT}"
  gcovr_command: 'gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..'
  cflags: ["-fstack-protector-strong", "-O0", "--sysroot=${SYSROOT}", "-B${LIBGCC_DIR}", "-L${LIB64_DIR}"]
  fuzz:
    output_root_dir: "fuzz_out"
    max_iterations: 256
    cfg_file_path: "${GCC_BUILD}/gcc/cfgexpand.cc.015t.cfg"
    use_qemu: true
    qemu_path: "qemu-aarch64"
    qemu_sysroot: "${SYSROOT}"
    timeout: 30
    max_constraint_retries: 8
  oracle:
    type: "canary"
    options:
      max_buffer_size: 1024
      default_buf_size: 64
      negative_cflags: ["-fno-stack-protector"]
targets:
  - file: "gcc/gcc/cfgexpand.cc"
    functions: ["stack_protect_classify_type", "expand_used_vars", "stack_protect_prologue"]
```

完整可运行样例：`@/home/yall/project/de-fuzz/configs/gcc-v15.2.0-aarch64-canary.yaml`。
