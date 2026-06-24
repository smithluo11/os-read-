# OS I/O Simulator — I/O 软件层 read 操作可视化模拟

> **操作系统课程设计 · 选题七** · I/O 软件层 `sys_read()` 全链路模拟 · 8 人小组

## 对应课设要求

本项目严格对标《操作系统课程设计》**选题七：I/O 软件层 read 操作模拟**，覆盖全部 4 项功能要求和 5 项验收标准：

| 课设要求 | 实现情况 |
|---------|---------|
| I/O 软件层分层模拟（用户层 → 设备无关 → 驱动 → 中断） | L0-L4 五层拓扑图 + SVG 连线 + IRP↓/IRQ↑/DATA↑ 标签 |
| read 全流程：请求转换 → 权限校验 → 指令下发 → 中断响应 → 结果返回 | 状态机逐步推进，每步附 stepDescription |
| 数据流转与缓冲模拟（单缓冲/双缓冲、用户态↔内核态数据传递） | 乒乓缓冲 A/B 切换 + 用户缓冲区面板 + 粒子动画 + 数据返回路径可视化 |
| 异常场景模拟（权限不足、非法地址、设备故障等） | 5 种故障注入：EACCES / EFAULT / EIO / EPERM / ENOENT |
| 单步执行清晰展示每步细节 | btn-step 单步 + 自动连点 + 系统调用日志（日志字体 13px） |
| 层级详情页（点击卡片跳转） | detail.html?layer=XX — 各层输入输出、数据结构、6 步操作流程、伪代码 |
| 8 人小组协作 + 答辩展示 | 见下方分工 |

## 项目背景

通过**可视化动画**直观展示一次 `sys_read(fd, buf, len)` 系统调用从用户空间到硬件层的完整流转过程，涵盖以下核心 OS 概念：

- **I/O 软件分层架构**：用户层 I/O → 设备无关层 (VFS) → 设备驱动 → 中断处理 → 硬件
- **层内子步骤可视化**：每层拆分为 3-4 个子步骤（共 17 步），层卡片显示 `n/m 步` 进度，日志格式 `[S2 (2/4)]`
- **L0 用户层 fd 表模拟**：进程打开文件表 (OpenFileEntry)、fd→file*→inode 查找链、access_ok() 地址校验、文件偏移量 (lseek) 追踪
- **双缓冲（乒乓缓冲）**：内核缓冲区 A/B 交替读写，提升 I/O 吞吐
- **IRP / IRQ 模型**：I/O 请求包下发与中断驱动的异步通知机制
- **进程状态转换**：RUNNING → BLOCKED → READY 的调度状态变迁
- **ACL 权限校验**：UID/GID 权限检查与路径穿越攻击防御
- **故障注入**：模拟 EACCES、EFAULT、EIO、EPERM、ENOENT、EBADF 等典型 I/O 错误

## 系统架构

```
┌─────────────────────────────────────────┐
│  前端 (Browser)                          │
│  ├─ 层级拓扑图 (5 层 OS 架构 + IRP↓/DATA↑) │
│  ├─ 层级详情页 (点击卡片跳转)              │
│  ├─ 粒子动画 (数据流方向指示)             │
│  ├─ 进程/内存/硬件 状态面板               │
│  └─ gRPC-Web WebSocket 双向流            │
└──────────────┬──────────────────────────┘
               │ WebSocket (bidi streaming)
┌──────────────▼──────────────────────────┐
│  后端 (Go)                               │
│  ├─ gRPC-Web Server (grpcweb.WrapServer) │
│  ├─ 状态机引擎 (internal/engine/)        │
│  │   ├─ ACL 文件系统校验                 │
│  │   ├─ 双缓冲乒乓读写控制器             │
│  │   └─ 分步状态推进 + 故障注入           │
│  └─ 静态文件服务 (前端页面)              │
└─────────────────────────────────────────┘
```

## 技术栈

| 层 | 技术 |
|---|------|
| 前端 | Vanilla JS + CSS Grid/Flexbox + SVG + Canvas |
| 通信 | gRPC-Web (Improbable) + WebSocket bidi streaming |
| 后端 | Go + gRPC + Protocol Buffers |
| 部署 | 二进制 + Nginx Proxy Manager 反向代理 |

## 快速开始

### 本地开发

```bash
# 后端
go run ./cmd/server/

# 前端（直接访问 Go 服务，它会同时提供静态文件）
open http://localhost:18083
```

### 构建前端 JS

```bash
make proto-js   # 生成 gRPC-Web JS bundle
```

### 修改 Proto 后重新生成

```bash
make proto      # 生成 Go pb 代码
make proto-js   # 生成 JS pb 代码 + bundle
```

## 云端部署

```bash
# 1. 交叉编译
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o io-sim-server ./cmd/server/

# 2. 上传到服务器
scp io-sim-server admin@<IP>:/home/admin/Os/
scp web/src/{index.html,detail.html,style.css,app.js,bundle.js} admin@<IP>:/home/admin/Os/web/src/

# 3. 服务器启动
cd /home/admin/Os
STATIC_DIR=/home/admin/Os/web/src nohup ./io-sim-server > backend.log 2>&1 &

# 4. NPM 反代 127.0.0.1:18083，开启 WebSocket
```

## I/O 软件层调用流程（5 层 × 3-4 子步骤 = 17 步）

```
L0 用户层 I/O 软件    [4 步] 库函数 fread() → fd 表查找 (fd→file*→inode) → access_ok() 校验 → 陷入内核 (int 0x80)
      │ IRP ↓
L1 设备无关软件 (VFS)   [4 步] 路径解析 → ACL 4 级权限校验 → 内核缓冲分配 → IRP 构造与下发
      │ IRP ↓
L2 设备驱动程序          [3 步] IRP 解析 → 内存映射 I/O 寄存器编程 → 进程阻塞 (BLOCKED)
      │ IRP ↓
L4 硬件层 (磁盘控制器)   [3 步] DMA 控制器初始化 → 磁盘扇区读取/DMA 传输 → 硬件中断 IRQ 发出
      │ IRQ ↑
L3 中断处理程序 (ISR)    [3 步] ISR 接管/上下文保存 → copy_to_user 数据拷贝 → 乒乓切换 / I/O 完成 → 文件偏移量更新
```

> 双缓冲模式下每 chunk 循环一轮 L2→L4→L3 (9 步)，4 chunk = 7 + 4×9 = 43 步。单步点击或自动连点 (50ms) 均可。

## 功能特性

- **双缓冲乒乓切换**：内核缓冲区 A 写入时 B 可同时被用户态读取
- **5 种故障注入**：权限拒绝、非法地址、硬件超时、路径穿越、文件不存在
- **用户上下文切换**：普通用户 (uid=1000) 与 Root (uid=0) 权限差异
- **粒子动画**：动态计算 DOM 坐标，可视化数据流方向
- **手风琴信息面板**：进程状态、用户缓冲区、内核差错控制台、系统调用日志
- **层级详情页**：点击拓扑卡片跳转 `detail.html?layer=XX`，展示每层输入/输出、关键数据结构、6 步操作流程、伪代码
- **数据返回路径可视化**：虚线钴蓝色连线（INT→DRV→VFS→USER）+ DATA↑ 标签，粒子动画动态指示数据流方向
- **6:4 宽屏布局**：左栏 OS 架构演示区居中，右栏信息面板弹性纵向排列，最大化利用宽屏空间

## 项目结构

```
.
├── api/
│   ├── proto/io_simulation.proto   # Protobuf 定义
│   └── pb/                         # 生成的 Go 代码
├── cmd/server/main.go              # 服务入口
├── internal/
│   ├── engine/                     # 核心状态机引擎
│   ├── filesystem/                 # ACL 文件系统校验
│   └── service/                    # gRPC 服务实现
├── web/src/
│   ├── index.html                  # 前端主页面（6:4 宽屏拓扑图）
│   ├── detail.html                 # 层级详情页（5 层动态渲染）
│   ├── style.css                   # 样式表
│   ├── app.js                      # 前端逻辑
│   ├── grpc-entry.js               # gRPC-Web 客户端
│   └── bundle.js                   # 打包后的 JS
└── nginx/default.conf              # Nginx 配置参考
```

## 小组分工（8 人）

| 角色 | 负责内容 |
|------|---------|
| 状态机引擎 (2人) | `internal/engine/` — sys_read 流程状态推进、双缓冲乒乓切换控制、分步状态快照生成 |
| 文件系统校验 (1人) | `internal/filesystem/` — ACL 权限检查 (UID/GID)、路径穿越攻击防御、文件存在性校验 |
| Proto 定义与通信层 (1人) | `api/proto/` — Protobuf 消息定义、gRPC-Web 双向流对接、WebSocket 帧编解码 |
| 前端可视化 (2人) | `web/src/` — SVG 五层拓扑图 + 连线动画 + 数据返回路径可视化、Canvas 粒子系统、手风琴状态面板、交互控制、层级详情页 |
| 前端样式与布局 (1人) | `web/src/style.css` + `index.html` + `detail.html` — 6:4 宽屏排版、浅色实验室主题、响应式适配、多页面导航 |
| 部署与文档 (1人) | 云端部署 (systemd + NPM)、README、答辩 PPT、现场 Demo 演示 |

## License

MIT · 操作系统课程设计 · 选题七 · 8 人小组
