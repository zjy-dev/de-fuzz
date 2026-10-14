# Podman 容器运行指南

本实现采用原生 rootless Pod：`defuzz-core-sandbox` 提供 gRPC/MCP，
`defuzz-orchestrator` 运行 Python、Pi SDK worker 和 CallGraph MCP。默认实验机制是 canary。
两个容器使用 Linux amd64 镜像；Apple Silicon 上由 Podman Machine 提供 Linux
虚拟机和 x86_64 转译，目标程序中的 ARM/RISC-V/LoongArch 再由显式 QEMU 执行。
性能数据应在固定硬件、固定转译环境下采集。

## 方案核对后的必要调整

原方案的职责划分和分层存储方向成立，以下调整来自实际代码和 Podman 行为：

| 原方案假设 | 实现 |
| --- | --- |
| Compose 自动得到原生 Pod 和共享 localhost | 使用 `podman kube play`；共享 Pod 网络，不映射宿主机端口 |
| Pod 共享 IPC 就能共享编译文件 | 两容器显式挂载同一个 tmpfs 命名卷，路径均为 `/tmp/defuzz-work` |
| Ubuntu 通用交叉 GCC 可替代实验编译器 | 保留 Debian 12 上冻结的 GCC17 四架构镜像及 QEMU 10.2 LoongArch |
| `--grpc-port`、`--work-dir`、`defuzz-campaign --phi` | 使用真实的 `--grpc-addr`、`TMPDIR`、`defuzz-loop run` / `defuzz-experiment pipeline` |
| 编排容器只需 Python | 正式 HTTP 流程会本地构建并执行 checker dispatcher，因此保留 Go 和同一套工具链 |
| 删除 `query_invariants` 等 MCP 工具 | 当前在线 agents 仍调用这些接口，本次保留；接口迁移需另行完成 |
| Rootless 彻底杜绝提权，tmpfs 保证零磁盘写入 | Rootless 降低权限，tmpfs 可交换到磁盘；不作绝对安全、性能或 AE 成功率承诺 |

现有 Go 反汇编路径使用 Go 库，不需要额外安装 Capstone。
Podman 6.1.3 实测会将 `emptyDir.medium: Memory` 转为容器各自的 tmpfs，且忽略
`sizeLimit`；本实现使用 PVC 的 `volume.podman.io/*` 注解创建共享的 local/tmpfs
卷，通过 `mount-options: size=2g,mode=1777` 实际限制容量。
交叉编译配置显式指定各架构 binutils、头文件和运行库目录；仅指向 GCC 的
`libexec` 目录不足以避免误用宿主架构汇编器。

## 首次准备

宿主机需要 Python 3.10+、Podman，以及预构建的实验工具链镜像和调用图数据库。
无需 `podman-compose`。

macOS 示例（已有 Podman Machine 时可复用其 rootless 连接）：

```sh
brew install podman
podman machine init --cpus 6 --memory 8192 --disk-size 80 defuzz
podman machine start defuzz
podman info --format '{{.Host.Security.Rootless}}'  # 应为 true
```

Linux 上以普通用户运行 Podman，准备好 subuid/subgid、容器存储和卷目录权限。
本机路径必须对 Podman 所在 Linux 主机可见。macOS 默认共享 `/Users`；
远程 Linux 连接不会自动上传宿主机的 bind mount 内容。

默认工具链是 CallGraph 匹配配置：

- GCC 16.2.0，发布提交 `78d4ac73dd391005b895a6148cd9831e28e1208b`，
  x86_64 与 AArch64。
- LLVM/Clang/LLD 23.1.2，发布提交
  `85ac560262434c9ccfc0c183ec22d4138ed647fb`；AArch64 使用明确选择
  `--target=aarch64-linux-gnu` 的驱动。
- 镜像 `localhost/defuzz-toolchains-matched:16.2.0-23.1.2`，构建脚本
  `scripts/toolchains/build-matched-image.py`；两个应用容器都继承该工具链。
- 源码位于 `/opt/defuzz-sources/gcc-16.2.0` 和
  `/opt/defuzz-sources/llvm-project-23.1.2`，对应发布包和实际文件校验和
  保存在 `/usr/local/share/defuzz/toolchains-matched.json`。
- 默认 `configs/toolchains.matched.container.yaml`；仅覆盖已匹配的两个 ISA。
  完整性要求不会因此改变，配置外的差分依赖必须显式补齐。

历史 GCC17 四 ISA 运行时保留，用于已冻结实验重放：

- GCC `17.0.0 experimental 20260531`，源码提交
  `f20bc4c2fe00928013c533e241b89ae3a6724ca1`。
- 本地镜像 `local/defuzz-gcc17-four-isa:20260531-qemu10.2`。
- 镜像 manifest digest：
  `sha256:3030327836aa7f15978a67b634912bd5aea38f35104fbbc1de0631033d2bd4d3`。
- 规范化 Docker archive 的 manifest digest：
  `sha256:ecf5edcde1853a85b4bf1d34115900f02fe275daeaa17fb6e665d08e25072e58`。
  两者 config ID 与全部 rootfs 层一致，构建脚本接受这两个经过核对的封装。
- 构建来源见 `scripts/toolchains/Dockerfile.gcc17-four-isa-runtime`；
  该镜像不是公开 registry 中可直接拉取的发行物，AE 发布时须一起提供镜像归档。

若已有本项目的 Docker 工具链镜像，可导入：

```sh
set -o pipefail
docker save local/defuzz-gcc17-four-isa:20260531-qemu10.2 \
  | python3 scripts/podman_import.py | podman load
```

`podman_import.py` 保留 Docker 的 `manifest.json` 和所有层，只移除双格式归档的
OCI 入口，避免 containerd 导出的混合媒体类型被 Buildah 拒绝；不会修改文件系统层。
从上述规范化归档恢复可直接执行 `podman load -i gcc17-four-isa.tar`。
构建脚本校验 GCC 提交和镜像 manifest digest；更换经过验证的工具链归档时，
显式传入 `--runtime-image` 与 `--runtime-digest`。
Docker 与 Podman 的 image ID 可能分别指 manifest/config，不能直接混用。

调用图数据库默认位于 `callgraph_mcp/data/callgraph.sqlite`；
生成方式见 [CallGraph README](../../../callgraph_mcp/README.md)。
缺少数据库时启动脚本会报错，不会悄悄创建空图。

## 构建、启动和验证

在仓库根目录执行：

```sh
python3 scripts/podman.py build
python3 scripts/podman.py up
python3 scripts/podman.py smoke
python3 scripts/podman.py status
```

构建应用镜像之前，准备并校验 lock 文件指定的源码包和 LLVM 官方二进制，
在 `.work/toolchains-matched` 放置 `gcc-source`、`llvm-source`、
`llvm-install`、`gcc-{x86_64,aarch64}-install/opt/gcc16`。GCC 交叉构建可用
`scripts/toolchains/build-gcc162-aarch64.sh`；本机原生 ARM Clang 加速构建用
`build-gcc162-native-host.sh`，随后在 amd64 runtime 构建/安装 target libgcc。
`build-matched-image.py` 拒绝缺失 GCC 驱动或不匹配的源码锚点，记录全部安装
文件 SHA-256 和基础镜像 ID，再生成匹配镜像。不要以旧工具链仅修改版本标签。

```sh
python3 scripts/toolchains/build-matched-image.py
python3 scripts/podman.py build
python3 scripts/podman.py exec -- python /workspace/docker/toolchain-smoke.py
```

最后一条在已启动 Pod 中验证 GCC/LLVM × x86_64/AArch64 编译、ELF machine、
实际执行和 MCP target 限制。历史四 ISA 配置可通过
`up --toolchains configs/toolchains.container.yaml` 显式选择；该 GCC17 配置无法
通过现有 GCC16 图的 Pi mapping/audit 预检。

构建脚本只发送白名单源码、版本化配置和锁文件，不发送 `.git`、本地模型
认证配置、调用图、实验输出或 `.env`。默认从 AWS ECR 的 Docker Official Images
镜像源拉取 Go 1.26.3 / Python 3.12.14 / Node 24.15.0，uv 固定为 0.11.14。
Pi coding-agent 和 pi-mcp 固定为 1.1.0，按 `agent-runtime/package-lock.json`
执行 `npm ci`、TypeScript 编译及生产依赖裁剪。
Python 依赖按各项目 `uv.lock` 执行 `uv sync --frozen`。构建暂存区仅将 PyPI
文件下载地址换成清华镜像，保留全部版本及哈希；仓库锁文件不变。
可用 `build --pypi-mirror https://files.pythonhosted.org` 使用原始下载源。
Go 默认使用可达的公共 `goproxy.cn` 并保留模块校验；
其他网络环境可以用 `build --go-proxy URL` 指定代理。
APT 默认使用清华 TUNA 的 HTTPS Debian 镜像并保留签名校验；
可用 `build --debian-mirror https://deb.debian.org` 切回官方源。

构建产生 `.work/podman/images.json`，记录基础/最终镜像标识、依赖镜像源和
构建暂存区文件的 SHA-256；同一构建记录写进镜像，在线 `manifest.json` 也包含它。
构建可用 `--engine docker`，但启动仍使用 Podman，需自行导入两张应用镜像。
macOS 远程客户端不能依赖 `kube play` 自动构建，因此先构建、再启动。

`smoke` 使用固定 C 种子、不调用模型，检查：

- 经 localhost gRPC 获取 checker 元数据，连续两轮配置内 ISA 的真实编译与 Oracle 判定；
- 在编排容器读取并执行 core 生成的 ELF；默认是 x86_64/AArch64；
- tmpfs 类型、只读调用图卷、MCP stdio 六个工具和 `list_targets`；
- 先归档再清理、失败编译目录回收、其他临时文件保留、归档 ELF 校验和。

启动参数可指定宿主机目录：

```sh
python3 scripts/podman.py up \
  --runs /absolute/path/runs \
  --callgraph /absolute/path/callgraph-data \
  --inputs /absolute/path/experiment-inputs \
  --toolchains /absolute/path/toolchains.container.yaml
```

`--inputs` 只读挂到 `/inputs`；其中的 `source/` 可供 core 的源码搜索使用。
其他机制须同时指定 `--mechanism ibt|fortify` 和相应的工具链配置。
默认配置的优化/防护选项是在线 canary 冒烟配置，正式实验须冻结自己的 flag profile。

## 运行实验

先验证无需模型的三阶段工程流程：

```sh
python3 scripts/podman.py exec -- defuzz-experiment pipeline \
  --config /workspace/configs/experiments/example.yaml
```

配置好 Pi 模型后，可运行在线循环。运行、模型和 MCP 配置文件放在 `--inputs`
目录，密钥通过进程环境传入；配置中不要写入密钥字面值。
例如宿主机已经设置 `DEFUZZ_COCONUT_API_KEY`，并准备好 `/inputs/pi-runtime.json`：

```sh
python3 scripts/podman.py exec --env DEFUZZ_COCONUT_API_KEY -- \
  defuzz-loop run --mechanism canary --budget-rounds 1 \
  --grpc localhost:50051 --mcp http://localhost:50052/mcp \
  --run-dir /workspace/runs --agent-cwd /workspace \
  --pi-config /inputs/pi-runtime.json \
  --disable-agent minimizer
```

当前 minimizer 的默认 `still-triggers` 命令尚未提供，安装 C-Reduce 并不会补齐
这个项目逻辑，所以示例显式禁用 minimizer。当前 gRPC coverage 注入的是空 backend，
也不能将该在线示例视为已采集真实覆盖率。

schema v2 论文正式流程使用 Pi backend，配置中设 `backend.kind: pi`：

```sh
python3 scripts/podman.py exec --env YOUR_API_KEY_ENV -- \
  defuzz-experiment pipeline --config /inputs/formal.yaml --show-plan
python3 scripts/podman.py exec --env YOUR_API_KEY_ENV -- \
  defuzz-experiment pipeline --config /inputs/formal.yaml
```

将 `YOUR_API_KEY_ENV` 替换为 Pi runtime `credential_env` 中的已设置变量名。
容器内网关地址应按实际网络配置；`127.0.0.1` 指向容器网络本身。

Pi 强隔离使用 bubblewrap 的 user/PID namespace，并隐藏 worker 的 `/proc`。
必须运行模型前的实际隔离预检；可执行文件存在不足以证明命名空间可用。
2026-10-12 本机 amd64 镜像在 AArch64 VM 的 QEMU 下无法运行该探针，
因此该模拟环境的正式 Pi 实验会拒绝启动。原生 AArch64 SDK 验证容器已通过，
其 rootless 运行使用 `--userns=keep-id` 和 `--security-opt seccomp=unconfined`；
隔离仍由 bubblewrap 执行。正式 GCC amd64 运行需原生 amd64 Linux 环境。
正式配置参照 `configs/experiments/formal.example.yaml`，使用容器内路径：
输出放 `/workspace/runs`；编译器源码、参考文献、checker 源码和 HTTP 配置
放在 `/inputs` 下；工具链可引用 `/workspace/configs/toolchains.yaml`。
输入目录中的符号链接目标也必须在容器内可达。
正式运行仍执行原有完整输入、干净 Git 工作树、冻结 verifier 和 token 统计检查。

若 HTTP 网关运行在 macOS 宿主机，应使用 `host.containers.internal`；
容器内 `localhost` 指向 Pod。网关需要能从 VM 访问，不能假定仅监听宿主机
127.0.0.1 的服务一定可达。
容器准备与固定种子/fixture 验证不等于完成论文正式实验。

## 存储、重放与停止

Pod 的两个容器共享 2 GiB tmpfs，各自默认限制为 3 CPU / 3 GiB 内存。
临时目录用尽会产生构建错误；按并发量调整 `docker/pod.yaml.in` 和 VM 资源。

在线记录位于宿主机 `runs/<实验>_<机制>_<时间>/`：

- `manifest.json`：源码/镜像、配置和 checker 信息；
- `events.jsonl`、`summary.json`：完成的轮次与最终状态；
- `rounds/NNNNNN/{seed.c,state.json,binary-*}`：源码、判定证据和二进制；
- `minimized.c`：仅在实际得到缩减结果时出现；
- `interrupted-state.json`：异常退出时的部分状态。

每轮在 oracle、可选 minimizer、feedback 全部结束且证据写入成功后，
只删除该轮 artifacts 所指向的 `DEFUZZ_WORK_ROOT/defuzz-build-*` 直接子目录。
归档失败不触发清理；异常中断的临时文件保留到移除 Pod，便于检查。
未设置 `DEFUZZ_WORK_ROOT` 的普通运行只做留存，不删除编译目录。
正式三阶段流程继续使用其原有产物生命周期。

在同一机制、同一镜像/工具链的 Pod 中重新判定归档二进制：

```sh
python3 scripts/podman.py exec -- python /workspace/docker/replay.py \
  /workspace/runs/EXPERIMENT/rounds/000000/state.json
```

脚本校验二进制 SHA-256 后调用确定性 Oracle；不会重放模型生成过程。
更换镜像或配置后的输出属于新环境下的复验。

```sh
python3 scripts/podman.py down
# macOS 不再使用该 VM 时：
podman machine stop defuzz
```

`down` 移除该 Pod 及其临时卷，宿主机 `runs/` 和只读输入保留。
要分发固定环境，请归档最终应用镜像、工具链镜像、`images.json` 和必要输入；
APT 仓库不是时间快照，因此重新构建不承诺逐字节相同，实验重放应使用归档镜像。

## 本机验收记录（2026-10-11）

环境：Apple Silicon macOS、Podman 6.1.3、rootless Machine（6 CPU / 8 GiB），
Linux amd64 镜像。最终镜像标识见 `.work/podman/images.json`。

| 检查 | 实测结果 |
| --- | --- |
| 镜像及工具 | 两张镜像构建成功；Python 3.12.14、Go 1.26.3、uv 0.11.14、OpenCode 1.18.31 可运行 |
| 本地 checker 构建 | 编排容器从源码成功构建 candidate dispatcher；Go 使用独立 `/opt/go`，避免混入基础镜像的旧标准库 |
| 网络与资源 | 同一 Pod 网络、无宿主机端口映射、无附加 capabilities；每容器 3 CPU / 3 GiB |
| 共享存储 | 同一 tmpfs 命名卷，实际容量 2 GiB；调用图卷写入返回 EROFS |
| 在线工程链路 | 两轮 × 四架构编译、ELF 执行、INV-SP-G01 确定性判定全部通过 |
| 归档与回收 | 每轮四个 ELF 的 SHA-256 正确；成功归档后清理，失败编译回收，其他临时文件保留 |
| 重放与重启 | 归档 ELF 可在 core 执行并重新判定；重启后 16 个归档文件哈希不变、临时标记消失 |
| MCP 与 fixture | core 列出五个工具，元数据查询通过；CallGraph 六个工具可列举；三阶段四变体 fixture 流程完成 |

最终镜像的冒烟证据：
[`smoke.json`](../../../runs/container-smoke_canary_20261010-173309-724390/smoke.json)，
同目录包含完整逐轮状态、源码和二进制。fixture 记录位于
`runs/pipelines/fixture-smoke-four-arm/`。
统一验收记录见 [`container-verification.json`](../../../runs/container-verification.json)，
包含最终镜像 ID、构建输入匹配结果、重启与服务检查。

本次没有调用模型执行正式实验。本机调用图当前实测为 3 个 target、80 条边，
正式运行须准备与目标编译器版本匹配的完整图；coverage backend 和 minimizer 的
现有限制见上文。验收结果用于确认容器工程链路，不作为论文实验结果。

## 核对来源

- [原始设计文档](https://bytedance.larkoffice.com/docx/BO75ddHaVo64xaxrnylcQIJ9n7g)
- [Podman kube play：支持字段、emptyDir、远程构建限制](https://docs.podman.io/en/latest/markdown/podman-kube-play.1.html)
- [Podman pod create：默认共享命名空间](https://docs.podman.io/en/latest/markdown/podman-pod-create.1.html)
- [Podman machine init：macOS VM 与目录挂载](https://docs.podman.io/en/latest/markdown/podman-machine-init.1.html)
- [项目正式实验执行规程](../../paper/experiment_runbook.md)
