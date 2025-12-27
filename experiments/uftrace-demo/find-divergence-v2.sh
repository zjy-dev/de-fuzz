#!/bin/bash
# 更精确的发散点查找 - 基于调用栈和时间序列

echo "=========================================="
echo "精确查找发散点"
echo "=========================================="
echo ""

# 导出完整的函数调用轨迹（包含进入/退出信息）
echo "=== 导出 trace1 的完整调用轨迹 ==="
uftrace replay -d trace1 --no-libcall -t 1us 2>/dev/null | \
    awk '/\[852229\]/ {print}' | \
    head -200 > trace1_full.txt
echo "已导出 $(wc -l < trace1_full.txt) 行"

echo ""
echo "=== 导出 trace2 的完整调用轨迹 ==="
uftrace replay -d trace2 --no-libcall -t 1us 2>/dev/null | \
    awk '/\[852492\]/ {print}' | \
    head -200 > trace2_full.txt
echo "已导出 $(wc -l < trace2_full.txt) 行"

echo ""
echo "=== 对比前50个函数调用 ==="
echo "trace1 (前20行):"
head -20 trace1_full.txt | sed 's/^/  /'
echo ""
echo "trace2 (前20行):"
head -20 trace2_full.txt | sed 's/^/  /'

echo ""
echo "=== 使用 Python 分析发散点 ==="
python3 << 'PYTHON'
import re

def parse_trace(filename):
    """解析 uftrace 输出，提取函数调用序列"""
    calls = []
    with open(filename, 'r') as f:
        for line in f:
            # 匹配函数调用行，例如: "  2.709 us [852229] |   getrlimit();"
            # 或者函数进入: "            [852229] | toplev::main() {"
            match = re.search(r'\|\s+(\S+)\(\)', line)
            if match:
                func_name = match.group(1)
                # 计算缩进深度（表示调用栈深度）
                indent_match = re.search(r'\|\s+', line)
                depth = len(indent_match.group(0)) - 1 if indent_match else 0
                calls.append((func_name, depth))
    return calls

print("解析 trace1...")
calls1 = parse_trace('trace1_full.txt')
print(f"找到 {len(calls1)} 个函数调用")

print("\n解析 trace2...")
calls2 = parse_trace('trace2_full.txt')
print(f"找到 {len(calls2)} 个函数调用")

print("\n查找第一个不同的函数调用...")
divergence_idx = -1
for i in range(min(len(calls1), len(calls2))):
    func1, depth1 = calls1[i]
    func2, depth2 = calls2[i]
    
    if func1 != func2 or depth1 != depth2:
        divergence_idx = i
        break

if divergence_idx >= 0:
    print(f"\n✓ 发散点在第 {divergence_idx + 1} 个函数调用:")
    print(f"  trace1: {calls1[divergence_idx][0]} (深度: {calls1[divergence_idx][1]})")
    print(f"  trace2: {calls2[divergence_idx][0]} (深度: {calls2[divergence_idx][1]})")
    
    print("\n发散前的调用上下文（最后10个相同的调用）:")
    for j in range(max(0, divergence_idx - 10), divergence_idx):
        print(f"  [{j+1}] {'  ' * calls1[j][1]}{calls1[j][0]}")
    
    print(f"\n发散后的不同路径:")
    print("  trace1 接下来调用:")
    for j in range(divergence_idx, min(divergence_idx + 5, len(calls1))):
        print(f"    [{j+1}] {'  ' * calls1[j][1]}{calls1[j][0]}")
    
    print("\n  trace2 接下来调用:")
    for j in range(divergence_idx, min(divergence_idx + 5, len(calls2))):
        print(f"    [{j+1}] {'  ' * calls2[j][1]}{calls2[j][0]}")
else:
    print("\n在前200行中未找到明显的发散点")
    print("两次执行的函数调用序列基本一致")

PYTHON

echo ""
echo "=========================================="
