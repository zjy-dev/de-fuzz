# DeFuzz CallGraph MCP

面向 GCC/LLVM 大型 CSV 调用图的只读 SQLite MCP。导入采用 10,000 行批次和事务，不把 CSV 全量读入内存；同一 target 再导入会原子替换。

## 安装与导入

```bash
python3 -m venv .venv
.venv/bin/pip install ./callgraph_mcp
.venv/bin/defuzz-callgraph-ingest --out callgraph.sqlite \
  --add llvm llvm-callgraph.csv \
  --add gcc-x86_64 gcc-x86_64-callgraph.csv \
  --add gcc-aarch64 gcc-aarch64-callgraph.csv
.venv/bin/defuzz-callgraph-mcp --db callgraph.sqlite
```

仓库脚本远程部署：

```bash
scripts/deploy_callgraph_mcp.sh              # 已有 DB 时不覆盖
scripts/deploy_callgraph_mcp.sh --code-only  # 仅同步/安装代码
scripts/deploy_callgraph_mcp.sh --force-ingest
```

OpenCode 通过 `scripts/run_callgraph_mcp_ssh.sh` 建立 SSH stdio；脚本不向 stdout 打印诊断。配置示例：

```json
{"mcp":{"defuzz-callgraph":{"type":"local","command":["./scripts/run_callgraph_mcp_ssh.sh"],"enabled":true}}}
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
