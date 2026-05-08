---
title: Scripts & Commands Reference
description: defuzz CLI 子命令、Makefile 目标、scripts/ 辅助脚本速查
priority: MEDIUM
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./tech-stack.md
  - ./config-schema.md
  - ../architecture/fuzz-engine-loop.md
---

# Scripts & Commands Reference

本页是命令速查；详细行为去对应文档读。

## 1. defuzz CLI

入口：`cmd/defuzz/main.go` → `cmd/defuzz/app/root.go`。

### `defuzz fuzz`

```bash
defuzz fuzz [--output DIR] [--log-dir DIR] [--limit N] [--timeout S] [--use-qemu]
```

| Flag | 默认 | 含义 | 配置覆盖 |
| --- | --- | --- | --- |
| `--output` | `fuzz_out` | 输出根目录；实际产物在 `{output}/{isa}/{strategy}` | `compiler.fuzz.output_root_dir` |
| `--log-dir` | `""` | 时间戳日志目录；空 = 仅 console | `log_dir` |
| `--limit` | `-1` (无限) | target BB 上限；`0` 仅跑初始 seed | `compiler.fuzz.max_iterations` |
| `--timeout` | `30` | 单次执行超时（秒） | `compiler.fuzz.timeout` |
| `--use-qemu` | `false` | 跨架构开关 | `compiler.fuzz.use_qemu` |

Examples:

```bash
defuzz fuzz                                  # 用 config.yaml 默认值
defuzz fuzz --limit 50                       # 限 50 个 target
defuzz fuzz --use-qemu --log-dir logs/       # AArch64 跨架构 + 文件日志
defuzz fuzz --limit 0                        # 仅处理初始 seeds（冒烟）
```

### `defuzz generate`

生成 understanding.md / function_template.c / 初始 seeds（基于 LLM）。读取 `cfg.Strategy` + `cfg.ISA`，往 `initial_seeds/<isa>/<strategy>/` 写入。

```bash
defuzz generate --strategy canary --isa aarch64
```

完整选项见 `cmd/defuzz/app/generate.go`。

## 2. Makefile

| 目标 | 命令 | 用途 |
| --- | --- | --- |
| `build` | `make build` | 构建 `bin/defuzz`（带版本元数据） |
| `build-debug` | `make build-debug` | `-N -l`，带调试信息 |
| `install` | `make install` | 装到 `$GOPATH/bin/` |
| `run` | `make run` | build + 立刻 `./defuzz`（无参，进 help） |
| `fmt` | `make fmt` | `gofmt -s -w ./cmd ./internal` |
| `lint` | `make lint` | `golangci-lint run`（需自装 lint） |
| `vet` | `make vet` | `go vet` |
| `tidy` | `make tidy` | `go mod tidy -e && go mod verify` |
| `test` | `make test` | `go test -short -race ./internal/...` |
| `test-v` | `make test-v` | 同上，verbose |
| `test-cover` | `make test-cover` | + HTML 报告 → `test-reports/coverage.html` |
| `test-integration` | `make test-integration` | 带 `integration` build tag；需要 gcovr/QEMU/uftrace |
| `test-bench` | `make test-bench` | benchmark；当前只有 `internal/coverage/` |
| `test-all` | `make test-all` | unit + integration + bench |
| `clean` | `make clean` | 删 build 产物 + test-reports |
| `clean-all` | `make clean-all` | + go cache + testcache |
| `help` | `make help` | 显示所有 target |

## 3. scripts/

### `scripts/stress_test.sh [num_copies]`

不调用 LLM 的 fuzzer 子系统压测：批量复制 `initial_seeds/aarch64/canary/` 下的 seed，串行跑 compile + coverage + oracle，测量"传统 fuzzer 链路"的吞吐。

```bash
./scripts/stress_test.sh 64       # 默认 64 副本
```

输出在 `stress_test_out/`。用途：定位 LLM 与 fuzzer 子系统的瓶颈贡献度。

### `scripts/llm_stress_test.sh <provider> [iterations]`

只测 LLM 端：用 8 个 unique prompt 调 LLM、看响应时延（绕开 LLM provider 的 prompt cache）。

```bash
./scripts/llm_stress_test.sh deepseek 8
./scripts/llm_stress_test.sh minimax 16
```

依赖 `.env` 里的 `DEEPSEEK_API_KEY` / `MINIMAX_API_KEY`。

### `scripts/qemu_vs_native_test.sh [iterations]`

比较 aarch64 在 QEMU user-mode 与 x86_64 native 上的 fuzz 链路吞吐。用来回答"开 QEMU 是否值得"。

```bash
./scripts/qemu_vs_native_test.sh 100
```

中间文件在 `/tmp/qemu_perf_test/`。

### `scripts/plot_coverage.py`

从 `fuzz_out/<isa>/<strategy>/state/state.json` 读 metrics，画两张图：

1. BB coverage 随 seed 增长曲线
2. 累积 bug 数

```bash
uv run scripts/plot_coverage.py --data-dir fuzz_out/loongarch64/canary
uv run scripts/plot_coverage.py -d fuzz_out/aarch64/canary -o aarch64
```

依赖 `uv` + matplotlib（脚本头部内嵌 PEP-723 metadata）。

## 4. 常用调试组合

```bash
# 1) 冒烟：只跑初始 seeds，确认配置 / 模板 / 编译可用
defuzz fuzz --limit 0 --log-dir logs/

# 2) 单 target 慢跑：看每个子系统日志
defuzz fuzz --limit 1 --use-qemu --log-dir logs/

# 3) 持久化检查点恢复：中断后再启动会自动从 total.json 续上
defuzz fuzz --limit 50 --log-dir logs/
# Ctrl-C 后再
defuzz fuzz --limit 50 --log-dir logs/

# 4) 重置：删 fuzz_out/{isa}/{strategy}/state/ 即重新开始
rm -rf fuzz_out/aarch64/canary/state/

# 5) 单元测试某个子包
go test -race ./internal/oracle/...
go test -race -run TestCanaryOracle_Analyze ./internal/oracle/

# 6) 过滤日志
grep 'oracle' logs/defuzz_*.log | head -20
```

## 5. 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 正常完成（无 bug 也算成功） |
| 1 | 配置 / 启动期错误（如 strategy/oracle mismatch） |
| 130 | Ctrl-C 中断（cobra/Go 默认） |

oracle 报 bug 的 seed 不影响 `defuzz fuzz` 的退出码——它只把 bug 写进 corpus。要做"bug 即失败" CI 集成时，外部脚本扫 `corpus/seed_*.json` 的 `OracleVerdict == "bug"` 字段。
