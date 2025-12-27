#!/bin/bash
# 更靠谱的发散点查找 v3
# 1. 自动提取 cc1 进程 ID
# 2. 过滤掉调度事件
# 3. 只关注编译阶段（c_parser_* 之后的函数）

set -e

TRACE1_DIR="${1:-trace1}"
TRACE2_DIR="${2:-trace2}"

echo "=========================================="
echo "发散点分析 v3"
echo "=========================================="
echo "trace1: $TRACE1_DIR"
echo "trace2: $TRACE2_DIR"
echo ""

# 自动提取 cc1 进程 ID
echo "=== 自动检测 cc1 进程 ID ==="
CC1_PID1=$(grep "cc1" "$TRACE1_DIR/task.txt" | head -1 | awk '{print $3}' | cut -d= -f2)
CC1_PID2=$(grep "cc1" "$TRACE2_DIR/task.txt" | head -1 | awk '{print $3}' | cut -d= -f2)
echo "trace1 cc1 PID: $CC1_PID1"
echo "trace2 cc1 PID: $CC1_PID2"
echo ""

# 导出函数调用（过滤掉 schedule 和 libcall）
echo "=== 导出调用序列（过滤调度事件）==="
uftrace replay -d "$TRACE1_DIR" --no-libcall 2>/dev/null | \
    grep "\[$CC1_PID1\]" | \
    grep -v "linux:schedule" | \
    grep -v "/\*" > trace1_filtered.txt
echo "trace1: $(wc -l < trace1_filtered.txt) 行"

uftrace replay -d "$TRACE2_DIR" --no-libcall 2>/dev/null | \
    grep "\[$CC1_PID2\]" | \
    grep -v "linux:schedule" | \
    grep -v "/\*" > trace2_filtered.txt
echo "trace2: $(wc -l < trace2_filtered.txt) 行"
echo ""

# 使用 Python 分析
python3 << 'PYTHON'
import re
import sys

def parse_trace(filename):
    """解析 uftrace 输出，只提取函数进入事件"""
    calls = []
    with open(filename, 'r') as f:
        for line in f:
            # 只匹配函数进入（有 { 的行）或简单调用（有 () 没有 } 的行）
            if '{' in line or ('()' in line and '}' not in line):
                # 提取函数名
                match = re.search(r'\|\s*(\S+)\s*\(', line)
                if match:
                    func_name = match.group(1)
                    # 跳过退出标记行
                    if func_name.startswith('}'):
                        continue
                    # 计算深度
                    pipe_pos = line.find('|')
                    if pipe_pos >= 0:
                        after_pipe = line[pipe_pos+1:]
                        depth = len(after_pipe) - len(after_pipe.lstrip())
                    else:
                        depth = 0
                    calls.append((func_name, depth // 2))  # 每2个空格一个深度
    return calls

print("解析 trace 文件...")
calls1 = parse_trace('trace1_filtered.txt')
calls2 = parse_trace('trace2_filtered.txt')
print(f"trace1: {len(calls1)} 个函数调用")
print(f"trace2: {len(calls2)} 个函数调用")

# 找到 c_parser 开始的位置（表示开始解析源代码）
def find_parser_start(calls):
    for i, (func, _) in enumerate(calls):
        if 'c_parser' in func or 'parse' in func.lower():
            return i
    return 0

start1 = find_parser_start(calls1)
start2 = find_parser_start(calls2)
print(f"\ntrace1 从第 {start1+1} 个调用开始解析源代码: {calls1[start1][0] if start1 < len(calls1) else 'N/A'}")
print(f"trace2 从第 {start2+1} 个调用开始解析源代码: {calls2[start2][0] if start2 < len(calls2) else 'N/A'}")

# 从解析阶段开始对比
calls1_parse = calls1[start1:]
calls2_parse = calls2[start2:]

print(f"\n只比较解析阶段的调用:")
print(f"  trace1: {len(calls1_parse)} 个调用")
print(f"  trace2: {len(calls2_parse)} 个调用")

# 找发散点
divergence_idx = -1
for i in range(min(len(calls1_parse), len(calls2_parse))):
    func1, depth1 = calls1_parse[i]
    func2, depth2 = calls2_parse[i]
    
    if func1 != func2:
        divergence_idx = i
        break

if divergence_idx >= 0:
    print(f"\n✓ 发散点在解析阶段第 {divergence_idx + 1} 个函数调用:")
    print(f"  trace1: {calls1_parse[divergence_idx][0]}")
    print(f"  trace2: {calls2_parse[divergence_idx][0]}")
    
    print("\n发散前的共同调用（最后5个）:")
    for j in range(max(0, divergence_idx - 5), divergence_idx):
        d = calls1_parse[j][1]
        print(f"  [{j+1}] {'  ' * d}{calls1_parse[j][0]}")
    
    print("\n发散后 trace1 的路径:")
    for j in range(divergence_idx, min(divergence_idx + 5, len(calls1_parse))):
        d = calls1_parse[j][1]
        print(f"  [{j+1}] {'  ' * d}{calls1_parse[j][0]}")
    
    print("\n发散后 trace2 的路径:")
    for j in range(divergence_idx, min(divergence_idx + 5, len(calls2_parse))):
        d = calls2_parse[j][1]
        print(f"  [{j+1}] {'  ' * d}{calls2_parse[j][0]}")
else:
    print("\n⚠ 在解析阶段未找到函数级别的发散点")
    print("两个程序的编译器执行路径在函数级别上是一致的")
    print("\n这可能意味着:")
    print("  1. 两个测试用例触发了相同的编译器代码路径")
    print("  2. 差异在更细粒度的基本块级别")
    print("  3. 需要结合 gcov 行覆盖数据来分析")

PYTHON

echo ""
echo "=========================================="
