#!/bin/bash
# 发散分析演示脚本

set -e

echo "=========================================="
echo "uftrace 发散分析演示"
echo "=========================================="
echo ""

# 1. 对比两次编译的总体差异
echo "=== 1. 对比编译时间差异 ==="
echo "trace1 (test1.c):"
uftrace info -d trace1 | grep "elapsed"
echo ""
echo "trace2 (test2.c):"
uftrace info -d trace2 | grep "elapsed"
echo ""

# 2. 对比函数调用次数差异
echo "=== 2. 查找调用次数差异最大的函数 ==="
uftrace report -d trace1 --diff trace2 --no-libcall -s call | grep -v "^#" | head -20
echo ""

# 3. 对比总运行时间差异
echo "=== 3. 查找运行时间差异最大的函数 ==="
uftrace report -d trace1 --diff trace2 --no-libcall -s total | grep -v "^#" | head -20
echo ""

# 4. 查找只在 trace1 中出现的函数
echo "=== 4. 只在 trace1 中调用的函数 ==="
uftrace report -d trace1 --diff-policy full --diff trace2 --no-libcall | \
    grep -E "^\s+[0-9]+\.[0-9]+ [a-z]+" | \
    awk '{if ($4 == "n/a") print}' | head -10
echo ""

# 5. 查找只在 trace2 中出现的函数
echo "=== 5. 只在 trace2 中调用的函数 ==="
uftrace report -d trace2 --diff-policy full --diff trace1 --no-libcall | \
    grep -E "^\s+[0-9]+\.[0-9]+ [a-z]+" | \
    awk '{if ($4 == "n/a") print}' | head -10
echo ""

echo "=========================================="
echo "演示完成！"
echo "=========================================="
