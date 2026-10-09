# DeFuzz CallGraph MCP

面向 GCC/LLVM 大型 CSV 调用图的只读 SQLite MCP。导入采用 10,000 行批次和事务，不把 CSV 全量读入内存；同一 target 再导入会原子替换。
项目内置了完整的静态数据集与预构建数据库（`callgraph_mcp/data/callgraph.sqlite`），支持本地 stdio 直接交互。

## 本地与标准 stdio 运行

项目内置预构建数据库，直接通过 local stdio 运行：

```bash
# 本地运行脚本（默认读取 callgraph_mcp/data/callgraph.sqlite）
./scripts/run_callgraph_mcp.sh
```

重新构建/生成内置数据集：

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

OpenCode 通过 `scripts/run_callgraph_mcp.sh` 建立本地 stdio 连接：

```json
{"mcp":{"defuzz-callgraph":{"type":"local","command":["./scripts/run_callgraph_mcp.sh"],"enabled":true}}}
```

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
