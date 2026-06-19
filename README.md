# OS I/O Simulator — VFS 与双缓冲可视化演示

> 操作系统课程小组项目 · I/O 子系统 `sys_read()` 调用全链路模拟

## 项目背景

本项目是操作系统课程小组作业，旨在通过**可视化动画**直观展示 Linux 内核中一次 `sys_read(fd, buf, len)` 系统调用从用户空间到硬件层的完整流转过程，帮助理解以下核心 OS 概念：

- **VFS（虚拟文件系统）**：设备无关层的抽象与路径解析
- **双缓冲（Double Buffering / 乒乓缓冲）**：内核缓冲区 A/B 交替读写，提升 I/O 吞吐
- **IRP / IRQ 模型**：I/O 请求包下发与中断驱动的异步通知机制
- **进程状态转换**：RUNNING → BLOCKED → READY 的调度状态变迁
- **ACL 权限校验**：UID/GID 权限检查与路径穿越攻击防御
- **故障注入**：模拟 EACCES、EFAULT、EIO、EPERM、ENOENT 等典型 I/O 错误

## 系统架构

```
┌─────────────────────────────────────────┐
│  前端 (Browser)                          │
│  ├─ 层级拓扑图 (5 层 OS 架构可视化)       │
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
scp web/src/{index.html,style.css,app.js,bundle.js} admin@<IP>:/home/admin/Os/web/src/

# 3. 服务器启动
cd /home/admin/Os
STATIC_DIR=/home/admin/Os/web/src nohup ./io-sim-server > backend.log 2>&1 &

# 4. NPM 反代 127.0.0.1:18083，开启 WebSocket
```

## 5 层 I/O 调用流程

```
L0 用户空间    sys_read(fd, buf, len)
      │ IRP ↓
L1 VFS 层      路径解析 → 权限校验 → 缓冲分配 (乒乓 A/B)
      │ IRP ↓
L2 驱动层      构造 I/O 请求包 → 下发硬件 → 进程阻塞
      │ IRP ↓
L4 硬件层      磁盘控制器执行 DMA 读取
      │ IRQ ↑
L3 中断层      ISR 中断服务唤醒进程 → 数据拷贝到用户缓冲区
```

## 功能特性

- **双缓冲乒乓切换**：内核缓冲区 A 写入时 B 可同时被用户态读取
- **5 种故障注入**：权限拒绝、非法地址、硬件超时、路径穿越、文件不存在
- **用户上下文切换**：普通用户 (uid=1000) 与 Root (uid=0) 权限差异
- **粒子动画**：动态计算 DOM 坐标，可视化数据流方向
- **手风琴信息面板**：进程状态、用户缓冲区、内核差错控制台、系统调用日志

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
│   ├── index.html                  # 前端主页面
│   ├── style.css                   # 样式表
│   ├── app.js                      # 前端逻辑
│   ├── grpc-entry.js               # gRPC-Web 客户端
│   └── bundle.js                   # 打包后的 JS
└── nginx/default.conf              # Nginx 配置参考
```

## 小组分工（建议）

| 角色 | 内容 |
|------|------|
| 状态机引擎 | `internal/engine/` — sys_read 流程状态推进、双缓冲控制 |
| 文件系统模块 | `internal/filesystem/` — ACL 权限、路径穿越防御 |
| 通信层 | `api/proto/` + gRPC-Web 双向流对接 |
| 前端可视化 | `web/src/` — SVG 拓扑图、粒子动画、状态面板 |
| 演示与文档 | README、答辩 PPT、现场 Demo 演示 |

## License

MIT · 操作系统课程作业
