---
title: FlagScheduler — 编译参数确定性轮转
description: canary 策略的多轴参数矩阵生成、按 target 轮转、1/20 negative-control 注入与对 oracle Polarity 的耦合
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../architecture/fuzz-engine-loop.md
  - ./canary-oracle.md
  - ../guides/cflags-configuration.md
  - ../reference/config-schema.md
---

# FlagScheduler — 编译参数确定性轮转

`internal/fuzz/flag_strategy.go` 的 `FlagScheduler` 是 fuzz 引擎里的一个独立调度子系统：在 LLM 把 seed 编译之前，按多维度笛卡儿积生成一组 `FlagProfile`，让每个 target BB 在不同的"参数姿势"下都被尝试求解，并且按固定节奏注入 negative-control（关闭防御）profile 当作对照组。

## 1. 设计目标

| 目标 | 实现手段 |
| --- | --- |
| **确定性**：同一 target 的第 N 次尝试必走同一 profile | `targetCursor[targetKey]` 取模 `len(mainProfiles)` |
| **覆盖矩阵**：policy × threshold × pic_mode × guard_mode | `buildProfilesForISA` 笛卡儿积构造，再去重 |
| **优先重要 profile**：`-fstack-protector-strong` + threshold=8 + 默认 pic + 默认 guard 排第一 | `profileRank` 八档手写优先级 + `lexicalAxisRank` fallback |
| **negative-control 校准**：每 20 个 target 注入一次"关防御"profile | `negativeControlInterval = 20` + `pendingNegative` map |
| **避免与 LLM 自由 cflags 冲突**：profile 维持的 flag family 屏蔽 LLM 同族 flag | `canaryLLMBlockedFlagFamilies` + `AllowLLMCFlags` 开关 |

## 2. 配置入口

YAML schema (节选自 `internal/config/flag_strategy.go`)：

```yaml
compiler:
  fuzz:
    flag_strategy:
      enabled: true                       # 主开关
      mode: "matrix"                      # 唯一支持的取值
      allow_llm_cflags: false             # 是否允许 LLM 自由 cflags 与 profile 共存
      include_negative_controls: true
      selection_order: "deterministic"    # 由 LoadConfig 默认填充
      axes:
        common:
          policy:    [["-fstack-protector"], ["-fstack-protector-strong"], ["-fstack-protector-all"], ["-fstack-protector-explicit"]]
          threshold: [["--param=ssp-buffer-size=1"], ["--param=ssp-buffer-size=8"], ["--param=ssp-buffer-size=32"]]
          pic_mode:  [[], ["-fPIC"]]
        by_isa:
          aarch64:
            guard_source: [[], ["-mstack-protector-guard=global"], ["-mstack-protector-guard=sysreg", "-mstack-protector-guard-reg=<config-provided-valid-sysreg>", "-mstack-protector-guard-offset=0"]]
      isa_options:
        aarch64:
          stack_protector_guard_reg: "x18"
      negative_controls:
        - ["-fno-stack-protector"]
```

`enabled: false` 或缺省该段时，`NewFlagScheduler` 返回 `(nil, nil)`，引擎走"无 profile"模式（FlagProfile 字段保持 nil）。

> **当前限制**：`buildProfilesForISA` 只支持 `isa == "aarch64"`，其它 ISA 直接报错。本文档同步发现的 follow-up 项之一。

## 3. 工作流

```
NewFlagScheduler(isa, cfg)                         flag_strategy.go:37
   ↓
buildProfilesForISA → mainProfiles (笛卡儿积 + sort + dedup)
                    + defaultProfile (= mainProfiles[0].Clone)
   ↓
buildNegativeProfile → negative                    (when include_negative_controls)
```

每轮 fuzz 主循环开始：

```
solveConstraint(target):
   FlagScheduler.BeginTarget(target)               # targetCount++; 每 20 个 target 标记 pendingNegative
   FlagScheduler.NextProfileForTarget(target, src) # 第一次返回 mainProfiles[0]，递增 cursor
   ...
   每次重试再调一次 NextProfileForTarget           # 同一 target 的不同姿势
```

`NextProfileForTarget` 实现（`flag_strategy.go:112-135`）：

1. 若 `pendingNegative[targetKey]` 为真且配置了 `negative` profile → 弹出并返回；
2. 否则从 `targetCursor[targetKey]` 起线性扫描 mainProfiles，跳过 `isProfileApplicable=false` 的（例如 `policy=explicit` 但 source 里没 `__attribute__((stack_protect))` 且不允许 LLM cflags）；
3. 命中 → cursor 推进到下一个；都不命中 → 返回 `defaultProfile.Clone()`。

## 4. 与 LLM 自由 cflags 的边界

被 profile 覆盖的 flag family 必须**不**让 LLM 同时输出，否则 GCC 拼出来的命令行歧义。`canaryLLMBlockedFlagFamilies`（`flag_strategy.go:16-23`）声明 6 个被 reserved 的 family：

```
-fstack-protector*
-fno-stack-protector*
--param=ssp-buffer-size=*
-fpic / -fPIC / -fpie / -fPIE
-mstack-protector-guard*
-fhardened
```

`Engine.attachPromptProfile` 把这份列表写到 `TargetContext.BlockedLLMFlagFamilies`，prompt builder 在生成 LLM prompt 时把它显式列为"绝对不能出现在 CFLAGS 段的 flag family"。`AllowLLMCFlags()` 还会被 compiler 层用作 `DisableLLMCFlags`（`compiler.go:filterLLMCFlags`），保证即便 LLM 不听话也不会真的传到编译器。

## 5. profile 排名规则

`profileRank`（`flag_strategy.go:324-356`）手写 8 档优先级：

| Rank | profile 特征 |
| --- | --- |
| 0 | strong + threshold-8 + default pic + default guard ← **defaultProfile** |
| 1 | strong + threshold-1 + default + default |
| 2 | strong + threshold-32 + default + default |
| 3 | all + threshold-8 + default + default |
| 4 | strong + threshold-8 + fPIC + default |
| 5 | strong + threshold-8 + default + global guard |
| 6 | strong + threshold-8 + default + sysreg-off0 |
| 7 | strong + threshold-8 + default + sysreg-off16 |
| 8 | explicit + threshold-8 + default + default |
| 100+ | 其它，按 `lexicalAxisRank` 字典序 |

排名稳定（`sort.SliceStable`），保证同一份 YAML 在不同机器上 cursor 行为完全一致。

## 6. 与 Oracle Polarizer 的耦合

`FlagScheduler` 选出 `IsNegativeControl=true` 的 profile 时：

1. profile 被 clone 进 `seed.FlagProfile`（`engine.go:716-727`）；
2. 编译时 LLM cflags 的注入被强制关闭（profile 自身就是定义"关防御"的一组 flag）；
3. `Engine.tryMutatedSeed` 检测到 `isNegativeProfile` 后**禁止**该 seed 进 corpus、不算新覆盖 (`engine.go:603-606, 651-665`)；
4. `CanaryOracle.polarityFor` 读到 `IsNegativeControl=true`，返回 `PolarityInverted`，`MechanismOracle` 据此对 polarity-sensitive checker 翻转 verdict（见 `tech-docs/architecture/oracle-mechanism-framework.md` §3）。

简言之：FlagScheduler 决定"哪 1/20 是负控"，Oracle Polarizer 决定"对负控如何解读 SIGSEGV / SIGABRT"。两者通过 `seed.FlagProfile` 这一个字段串起。

## 7. 故障域速查

| 现象 | 排查 |
| --- | --- |
| 启动报 `flag strategy currently supports only aarch64 canary` | `cfg.ISA` 不是 `aarch64` 但 `flag_strategy.enabled=true`；当前未实现其它 ISA |
| 启动报 `policy, threshold and pic_mode axes are required` | YAML 里 `axes.common` 缺字段；按 §2 schema 补齐 |
| 主循环里负控明显多于 1/20 | 检查 `pendingNegative` 是否被外部 mutate；正常情况下只在 `targetCount % 20 == 0` 设 true |
| 同一 target 反复用同一 profile | `targetCursor[key]` 没推进 → 看 `NextProfileForTarget` 是否每次都因 `isProfileApplicable=false` 落到 fallback |

代码：`@/home/yall/project/de-fuzz/internal/fuzz/flag_strategy.go`、配置：`@/home/yall/project/de-fuzz/internal/config/flag_strategy.go`、配套样例：`@/home/yall/project/de-fuzz/configs/gcc-v15.2.0-aarch64-canary.yaml`。
