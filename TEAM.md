# 小组分工方案

> 组长罗一航已完成项目架构、全部代码、部署和文档。以下是 7 个可选的模块分工，每位组员**认领一个模块**，负责理解对应代码、准备答辩 QA、必要时微调文案/样式。

---

## 分工总览

| 编号 | 模块 | 难度 | 适合 |
|------|------|------|------|
| [A] | 状态机引擎核心 | ★★★★ | 想深入理解 OS I/O 流程的同学 |
| [B] | 文件系统校验与 ACL | ★★★ | 对 Linux 权限/安全感兴趣的同学 |
| [C] | Proto 协议与 gRPC-Web 通信 | ★★★★ | 想了解 RPC/序列化的同学 |
| [D] | 前端拓扑图与 SVG 连线动画 | ★★★ | 喜欢视觉/前端动效的同学 |
| [E] | 前端快照渲染与交互控制 | ★★★ | 对 JS 事件驱动感兴趣的同学 |
| [F] | 前端样式体系与层级详情页 | ★★ | 对 CSS 布局/设计感兴趣的同学 |
| [G] | 部署运维、测试与答辩 PPT | ★★ | 对 DevOps/运维感兴趣的同学 |

---

## [A] 状态机引擎核心

**文件**: `internal/engine/engine.go`（320 行）

**负责内容**:
- 五层递进状态机（USER → VFS → DRV → HW → INT）的设计与 NextStep 推进逻辑
- 双缓冲乒乓切换算法：ActiveWriteBuffer / ActiveReadBuffer 的 1↔2 翻转、chunk 分块 (4KB)、DMA 调度
- 5 种故障注入的拦截点与错误码映射（EACCES / EFAULT / EIO / EPERM / ENOENT）
- 进程状态转换：RUNNING → BLOCKED（等待中断）→ RUNNING
- 硬件层模拟：磁盘寻道、扇区读取、CMD_REG / STS_REG / DATA_REG 寄存器操作
- ISR 层逻辑：数据从硬件寄存器 → 内核缓冲区 → copy_to_user → 乒乓判断 → 是否继续循环

**答辩关键词**: 状态机、中断驱动 DMA、乒乓缓冲、进程阻塞、IRP/IRQ

---

## [B] 文件系统校验与 ACL

**文件**: `internal/engine/filesystem.go`（132 行）

**负责内容**:
- 模拟 Linux VFS 层的四级安全校验管线：
  1. 路径穿越检测（`..` 模式匹配 + 规范化解析）
  2. 文件存在性检查（FileSystemDB 查表）
  3. 敏感文件保护（`/etc/shadow` 等仅 root 可读）
  4. Unix 9 位权限位检查（owner/group/other × rwx）
- root (uid=0) 绕过所有权限检查的实现
- 文件系统数据库设计（`FileSystemDB` map，含 OwnerUID/GroupGID/Permissions/IsSensitive）
- 校验错误类型 `ValidationError` 的 Code + Description 设计

**答辩关键词**: VFS、ACL、路径穿越攻击、Unix 权限位、root 特权

---

## [C] Proto 协议与 gRPC-Web 通信

**文件**: `api/proto/io_simulation.proto`（87 行）+ `internal/service/handler.go`（88 行）+ `web/src/grpc-entry.js`（192 行）

**负责内容**:
- Protobuf 消息体系设计：SimControlCommand（3 种 Action）、SystemSnapshot（5 层 Layer 枚举 + 嵌套 ProcessBlock/MemoryView/HardwareView）
- gRPC 双向流 RPC 定义：`stream SimControlCommand returns stream SystemSnapshot`
- Go 后端 handler：`ACTION_INIT` 初始化引擎并推送初始快照、`ACTION_STEP_NEXT` 推进状态机并回传快照、`ACTION_INJECT_FAULT` 运行时注入故障
- **前端帧编解码器**：手写 Improbable gRPC-Web WebSocket 协议帧格式 —— `[control=0][flag][4B BE len][pb data]`，包括 frame/parse 函数、消息队列、粘包缓冲处理
- WebSocket 握手：首帧 ASCII header 块（`Content-Type: application/grpc-web+proto`）

**答辩关键词**: Protobuf、gRPC 双向流、WebSocket 帧格式、序列化/反序列化

---

## [D] 前端拓扑图与 SVG 连线动画

**文件**: `web/src/index.html`（SVG 部分 + 层卡片结构）

**负责内容**:
- SVG viewBox 坐标系设计（`0 0 720 750`），所有 `<line>` 坐标手工计算
- 五层卡片（L0~L4）的绝对定位居中体系（`left:50%; transform:translateX(-50%)`）
- 下行连接线（IRP ↓）：USER→VFS→DRV→HW，通过 `.connector.active` 类实现高亮联动
- 上行连接线（IRQ ↑）：HW→INT→DRV，ISR 响应后的回路
- 数据返回路径（DATA ↑）：DRV→VFS→USER，钴蓝色虚线 + `return-path` 类 + 有数据时点亮
- `IRP ↓` / `IRQ ↑` / `DATA ↑` 三个 `<foreignObject>` 标签与连线联动
- 多层放大历程：卡片尺寸从 320px → 500px/560px 的演进

**答辩关键词**: SVG、viewBox、绝对定位居中、有向连线动画、拓扑可视化

---

## [E] 前端快照渲染与交互控制

**文件**: `web/src/app.js`（320 行）

**负责内容**:
- `onSnapshot` 快照渲染函数：将 SystemSnapshot protobuf 消息映射到 DOM 更新
  - 活动层高亮（`.layer-card.active`）+ 连线激活（`.connector.active`）
  - 进程状态渲染（RUNNING/BLOCKED/READY 徽章切换）
  - 内核缓冲区数据更新（`updateKbuf` — write/read 类切换）
  - 用户缓冲区累加显示（`ubuf-display` + `ubuf-len`）
  - 硬件寄存器展示（CMD_REG / STS_REG / DATA_REG）
  - 进度条更新（`pbar.fill` + `chunk-stat`）
- Canvas 粒子系统：贝塞尔曲线轨迹、`getBoundingClientRect` 动态坐标计算、`requestAnimationFrame` 动画循环、乒乓切换触发粒子
- 连接状态机：Connect → Init → Step 按钮联动、自动连点 (50ms 间隔)、故障注入联动
- 手风琴面板折叠动画 + 日志卡片 flex 弹性扩展
- 层级卡片点击跳转 (`detail.html?layer=XX`)

**答辩关键词**: 快照渲染、DOM 驱动更新、Canvas 粒子动画、贝塞尔曲线、事件绑定

---

## [F] 前端样式体系与层级详情页

**文件**: `web/src/style.css` + `web/src/detail.html`

**负责内容**:
- CSS 变量设计体系（`--phosphor` / `--cobalt` / `--ink` / `--border` / `--bg-panel` 等）
- 6:4 CSS Grid 宽屏主布局 + 右侧面板 flex 纵向弹性链（`flex:1` + `height:0` + `min-height:0` 解决溢出）
- 顶部控制栏 72px + 底部 footer 32px + `calc(100vh - 72px - 32px)` 精确高度管控
- 层级卡片样式：胶囊标签 (L0~L4)、悬停 glow 效果、active/error 状态切换
- 手风琴组件：`.accordion.open` 控制展开、`.accordion-arrow` 箭头旋转动画
- 连接线样式：实线 IRP/IRQ (橙黄) + 虚线 DATA↑ (钴蓝) + 反向动画
- 进度条填充动画 `.pbar-fill`、缓冲区 write/read 色块、内核差错控制台色彩标识
- **详情页**: `detail.html` 内联 CSS + JS，6:4 布局，根据 `?layer=` URL 参数动态渲染 5 层内容（每层含输入/输出、数据结构、6 步操作、伪代码高亮）、迷你拓扑位置图、返回按钮

**答辩关键词**: CSS Grid/Flexbox、变量体系、响应式布局、动态内容渲染、手风琴组件

---

## [G] 部署运维、测试与答辩 PPT

**负责内容**:
- 本地开发环境：`go run ./cmd/server/` → `localhost:18083`
- 跨平台编译：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build` 生成无依赖静态二进制
- 云端部署流程：SCP 上传 → systemd 服务配置 → Nginx Proxy Manager 反代 + WebSocket Upgrade
- systemd unit 编写（ExecStart / Restart=always）
- NPM 配置要点：WebSocket 支持（`proxy_http_version 1.1` + `Upgrade $http_upgrade`）
- 测试用例设计与执行（正常流 3 场景 + 故障注入 5 场景 + 权限边界 4 场景 + 前端集成 6 项）
- 项目仓库管理（Git 工作流、分支策略、commit 规范）
- 答辩 PPT 制作：项目背景 → OS 理论 → 架构设计 → 技术选型 → 核心亮点 → Demo 演示 → 小组协作
- 演示录制 / 现场 Demo 操作流程

**答辩关键词**: 跨平台部署、systemd、NPM 反代、集成测试、答辩演示

---

## 认领说明

1. 每人在群里报两个志愿（第一志愿 + 第二志愿），组长根据志愿分配
2. 认领后请**阅读对应代码文件**，不理解的地方随时问组长
3. 答辩时每个人需要能讲清楚自己模块的**设计思路**和**与 OS 课本知识的对应关系**
4. 模块 C（通信层）和 A（引擎核心）难度较高，建议有 Go/网络编程基础的同学选
5. 模块 F 和 G 难度较低但答辩需要展示视觉/文档成果

---

## 组长已完成的工作

| 类别 | 内容 |
|------|------|
| 项目架构 | 前后端分离 + gRPC-Web 双向流 + 五层状态机的整体设计 |
| Go 后端 | `cmd/server/`、`internal/engine/`、`internal/service/` 全部代码 |
| Proto 定义 | `api/proto/io_simulation.proto` + `make proto` 代码生成 |
| 前端 | `index.html`、`style.css`、`app.js`、`grpc-entry.js`、`detail.html` 全部代码 |
| 部署 | 云服务器部署 + systemd + NPM 配置 |
| 文档 | README.md + DESIGN.md 设计文档 |
| Git | 仓库管理 + 全部 commit |
