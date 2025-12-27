#!/bin/bash
# 找到两次执行的发散点（第一个不同的函数调用）

set -e

echo "=========================================="
echo "查找发散点 - 第一个不同的函数调用"
echo "=========================================="
echo ""

# 导出两个 trace 的函数调用序列（只看 cc1 进程，这是实际编译器）
echo "=== 导出 trace1 的调用序列 ==="
uftrace replay -d trace1 --no-libcall -F main 2>/dev/null | \
    grep -E '^\s+[0-9]+\.[0-9]+ [a-z]+s \[[0-9]+\] \|' | \
    awk '{for(i=5;i<=NF;i++) printf "%s ", $i; printf "\n"}' | \
    head -100 > trace1_calls.txt
echo "已导出到 trace1_calls.txt (前100个调用)"

echo ""
echo "=== 导出 trace2 的调用序列 ==="
uftrace replay -d trace2 --no-libcall -F main 2>/dev/null | \
    grep -E '^\s+[0-9]+\.[0-9]+ [a-z]+s \[[0-9]+\] \|' | \
    awk '{for(i=5;i<=NF;i++) printf "%s ", $i; printf "\n"}' | \
    head -100 > trace2_calls.txt
echo "已导出到 trace2_calls.txt (前100个调用)"

echo ""
echo "=== 使用 diff 查找第一个差异 ==="
diff -u trace1_calls.txt trace2_calls.txt | head -50 || true

echo ""
echo "=== 查找发散点 ==="
python3 << 'PYTHON'
# 读取两个调用序列文件
with open('trace1_calls.txt', 'r') as f:
    calls1 = [line.strip() for line in f if line.strip()]

with open('trace2_calls.txt', 'r') as f:
    calls2 = [line.strip() for line in f if line.strip()]

# 找到第一个不同的调用
divergence_found = False
for i in range(min(len(calls1), len(calls2))):
    if calls1[i] != calls2[i]:
        print(f"发散点在第 {i+1} 个函数调用:")
        print(f"  trace1: {calls1[i]}")
        print(f"  trace2: {calls2[i]}")
        print("")
        
        # 显示发散前的上下文
        print("发散前的调用序列（最后5个相同的调用）:")
        for j in range(max(0, i-5), i):
            print(f"  [{j+1}] {calls1[j]}")
        
        divergence_found = True
        break

if not divergence_found:
    print("在前100个调用中未找到明显的发散点")
    print(f"trace1 有 {len(calls1)} 个调用")
    print(f"trace2 有 {len(calls2)} 个调用")
PYTHON

echo ""
echo "=========================================="
echo "分析完成！"
echo "=========================================="
