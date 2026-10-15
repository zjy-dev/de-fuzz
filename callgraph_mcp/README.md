# DeFuzz CallGraph MCP

面向 GCC/LLVM 大型 CSV 调用图的只读 SQLite MCP。导入采用 10,000 行批次和事务，不把 CSV 全量读入内存；同一 target 再导入会原子替换。
项目内置了由 CodeQL 从真实编译器构建过程抽取的完整静态调用图数据集（`callgraph_mcp/data/csv/`）与预构建数据库（`callgraph_mcp/data/callgraph.sqlite`），完全本地、离线，支持本地 stdio 直接交互。

内置 target 与规模：

| target | 边数 |
|---|---|
| `gcc-16.2.0-x86_64` | 579,916 |
| `gcc-16.2.0-aarch64` | 582,555 |
| `llvm-23.1.2` | 2,025,915 |

## 本地与标准 stdio 运行

项目内置预构建数据库，直接通过 local stdio 运行：

```bash
# 本地运行脚本（默认读取 callgraph_mcp/data/callgraph.sqlite）
./scripts/run_callgraph_mcp.sh
```

从内置真实 CSV 重新构建数据库：

```bash
cd callgraph_mcp && uv run python build_dataset.py
```

或使用自定义 CSV 进行导入：

```bash
python3 -m venv .venv
.venv/bin/pip install ./callgraph_mcp
.venv/bin/defuzz-callgraph-ingest --out data/callgraph.sqlite \
  --add llvm-23.1.2 data/csv/llvm-23.1.2.csv \
  --add gcc-16.2.0-x86_64 data/csv/gcc-16.2.0-x86_64.csv \
  --add gcc-16.2.0-aarch64 data/csv/gcc-16.2.0-aarch64.csv
.venv/bin/defuzz-callgraph-mcp --db data/callgraph.sqlite
```

Pi SDK 通过显式 MCP 配置建立本地 stdio 连接，运行时解析项目绝对路径：

```json
{"mcpServers":{"defuzz_callgraph":{"command":"${PROJECT_ROOT}/scripts/run_callgraph_mcp.sh","args":[],"cwd":"${PROJECT_ROOT}","database_path":"${PROJECT_ROOT}/callgraph_mcp/data/callgraph.sqlite","provenance_path":"${PROJECT_ROOT}/callgraph_mcp/data/provenance.json"}}}
```

## 编译器版本绑定

`data/provenance.json` 和 SQLite 的 `provenance` 表记录发布版本、CSV/query
哈希、原始 CodeQL 构建日志哈希，以及发布源码中被图引用的文件哈希。
GCC 16.2.0 对应发布提交 `78d4ac73dd391005b895a6148cd9831e28e1208b`；
LLVM 23.1.2 对应 `85ac560262434c9ccfc0c183ec22d4138ed647fb`。
原始归档没有 extractor checkout 的 commit，因此这些提交是从发布标签和原始
构建日志推定的身份，不能声称已独立证明 extraction 时的全部源码字节。
源码锚点校验将当前输入与官方发布源码匹配；它不补造缺失的历史证据。

实际图覆盖是 GCC x86_64/AArch64 和 LLVM X86/AArch64。LLVM CSV 包含其他
target 名字的符号，不等于其他后端已经参与 CodeQL 构建。
GCC17、RISC-V、LoongArch 需要另外生成对应图，不能改名字复用旧图。

Pi 的正式 `pipeline --show-plan`、独立 mapping/audit 和 `run-all` 在模型调用前
检查源码版本、锚点字节、ISA 覆盖及配置驱动版本，并冻结数据库 SHA-256。
worker 仅获得绑定 target，MCP 的 `--allow-target` 同时过滤 `list_targets`
并拒绝其他 target 查询。每次调用记录 `callgraph-binding.json`。
缺失 metadata 的 MCP 配置只供注入的测试 fixture 使用，生产 CLI 会拒绝。

匹配的容器配置是 `configs/toolchains.matched.container.yaml`，历史 GCC17
配置仍保留在 `configs/toolchains.container.yaml`。容器构建与验证见
[运行指南](../docs/tech-docs/guides/podman.md)。

## 六个工具

所有结果均含 `query`、`counters`、`truncated`；边包含 `caller/callee/edge_kind/file/line`。

| Tool | 参数约束 | 用途 |
|---|---|---|
| `list_targets()` | 无 | target、来源和边数 |
| `find_function(target, pattern, limit=100)` | `limit<=500` | 字面子串消歧 |
| `get_callees(target, function, depth=1, limit=5000)` | `depth<=5`, `limit<=5000` | BFS 下游调用 |
| `get_callers(target, function, depth=1, limit=5000)` | 同上 | BFS 上游调用 |
| `slice_from(target, entry, depth=3, max_nodes=2000)` | `depth<=5`, `max_nodes<=2000` | 防环前向切片 |
| `paths_between(target, src, dst, max_depth=6, max_paths=50)` | `max_depth<=8`, `max_paths<=50` | 反向最短距离剪枝的有界 BFS |

推荐查询：先 `list_targets`，再用 `find_function` 找到精确符号；随后用一层 callers/callees 验证方向，逐步增大深度；仅在入口明确时做 slice，在源终点均明确时查 paths。不要一开始使用最大预算。
