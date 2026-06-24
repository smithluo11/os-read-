# 小组分工方案

> 以下是 7 个模块的详细分工，每位组员认领一个模块，负责理解对应代码、准备答辩 QA、必要时微调文案/样式。

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

**文件**: `internal/engine/engine.go`（644 行）

**负责内容**:

- **五层 × 子步骤双层状态机**：L0 用户层 (4 子步骤) → L1 VFS 层 (5 子步骤) → L2 驱动层 (3 子步骤) → L4 硬件层 (3 子步骤) → L3 中断层 (3 子步骤)，共 18 子步骤。单缓冲完整 18 步，双缓冲 4KB×N chunk 循环
- **L0 用户层数据结构**：进程打开文件表 `FdTable []*OpenFileEntry`（Fd / FilePath / Flags / FilePos / InodePtr），fd→file*→inode 查找链，access_ok() 地址范围校验，文件偏移量 FilePos 追踪（模拟 lseek）
- **双缓冲乒乓切换算法**：ActiveWriteBuffer / ActiveReadBuffer 的 1↔2 翻转、chunk 分块 (4096 字节)、多轮 DMA 循环调度
- **6 种故障注入**：EACCES（权限拒绝）、EFAULT（非法地址）、EIO（硬件超时）、EPERM（路径穿越）、ENOENT（文件不存在）、**EAGAIN（设备忙可重试，非终态，最多 3 次重试，NextStep 通过 retrySubStep 标志暂停子步骤推进）**。每种故障有精确的子步骤拦截点
- **页缓存 (Page Cache)**：L1 子步骤 3 查询 `PageCache[filePath]`，命中直接返回（跳过 L2/L3/L4），未命中走磁盘 I/O 后在 L3 子步骤 3 回写
- **进程状态转换**：RUNNING → BLOCKED（等待磁盘 I/O）→ READY → RUNNING
- **DMA 控制器模拟**：源地址 / 目标地址 / 传输计数 / 状态 (SETUP→TRANSFERRING→DONE) 的完整生命周期
- **硬件层模拟**：CMD_REG / STS_REG / DATA_REG 寄存器操作、磁盘寻道、扇区读取、IRQ 发送
- **ISR 层逻辑**：上下文保存 → 硬件寄存器到内核缓冲区 → copy_to_user → 乒乓判断 → 页缓存回写

**答辩关键词**: 状态机、子步骤、中断驱动 DMA、乒乓缓冲、页缓存、EAGAIN 重试、进程阻塞、IRP/IRQ

---

## [B] 文件系统校验与 ACL

**文件**: `internal/engine/filesystem.go`（132 行）

**负责内容**:
- 模拟 Linux VFS 层的**四级安全校验管线**：
  1. 路径穿越检测（`..` 模式匹配 + 规范化解析，防御目录逃逸攻击）
  2. 文件存在性检查（FileSystemDB 查表）
  3. 敏感文件保护（`/etc/shadow` 等仅 root 可读）
  4. Unix 9 位权限位检查（owner/group/other × rwx，严格按 UID→GID→other 顺序判定）
- root (uid=0) 绕过所有权限检查的实现
- 文件系统数据库设计（`FileSystemDB` map，含 OwnerUID / GroupGID / Permissions / IsSensitive 字段）
- 校验错误类型 `ValidationError` 的 Code + Description 设计，与 engine.go L1 子步骤 2 集成
- `internal/engine/filesystem_test.go` 中的权限边界测试用例（跨用户、跨文件、路径穿越等场景）

**答辩关键词**: VFS、ACL、路径穿越攻击、Unix 权限位、root 特权、四级安全管线

**与 engine.go 的关系**：`ValidateFileAccess()` 被 engine.go 的 `executeIndependentLayer()` 在 L1 子步骤 2 调用，校验失败时设置 `FinalErrorCode` 并终止模拟。

---

## [C] Proto 协议与 gRPC-Web 通信

**文件**: `api/proto/io_simulation.proto`（100 行）+ `api/pb/`（自动生成）+ `internal/service/handler.go`（110 行）+ `web/src/grpc-entry.js`（197 行）+ `web/src/bundle.js`（esbuild 打包）

**负责内容**:
- **Protobuf 消息体系设计**（9 个 message + 1 个 enum + 1 个 service）：
  - `SimControlCommand`（3 种 Action：INIT / STEP_NEXT / INJECT_FAULT）
  - `ReadRequestConfig`（含 `use_page_cache` 布尔开关）
  - `FaultType` 枚举（6 种故障类型，含 `FAULT_EAGAIN`）
  - `SystemSnapshot`（含 `sub_step` / `total_sub_steps` 子步骤追踪）
  - `MemoryView`（含 `cache_hit` / `cached_pages` / `retry_count` / `retry_max`）
  - `HardwareView`（含 `dma_source` / `dma_destination` / `dma_count` / `dma_status` 四个 DMA 寄存器）
- **gRPC 双向流 RPC 定义**：`rpc StreamSimulation(stream SimControlCommand) returns (stream SystemSnapshot)`
- **Go 后端 handler 逻辑**：
  - `ACTION_INIT`：提取 UserContext → 跨 INIT 持久化页缓存 → 创建新引擎 → 推送初始快照
  - `ACTION_STEP_NEXT`：已完成的模拟忽略多余请求 → 调用 NextStep() → 首次完成时推送最终快照
  - `ACTION_INJECT_FAULT`：运行时注入故障类型到引擎
- **前端帧编解码器**：手写 Improbable gRPC-Web WebSocket 协议帧格式 —— `[control=0][flag][4B BE len][pb data]`，包括 frame / parse 函数、消息队列（握手前缓存）、粘包缓冲处理、trailer 帧（grpc-status）解析
- **WebSocket 生命周期**：首帧 ASCII header 块（`Content-Type: application/grpc-web+proto`）、onOpen / onSnapshot / onError / onEnd 回调链
- **Proto 代码生成**：`make proto`（Go pb）+ `make proto-js`（JS pb + esbuild 打包为 `bundle.js`）

**答辩关键词**: Protobuf、gRPC 双向流、WebSocket 帧格式、序列化/反序列化、esbuild 打包、帧编解码器

---

## [D] 前端拓扑图与 SVG 连线动画

**文件**: `web/src/index.html`（SVG 部分 + 层卡片结构）

**负责内容**:
- **SVG viewBox 坐标系设计**（`0 0 720 750`），所有 `<line>` 坐标计算
- **五层卡片定位**（L0~L4）：`left:50%; transform:translateX(-50%)` 绝对居中，各层宽度不同（USER=500px, VFS=560px, DRV=500px, HW/INT=330px）
- **三层连线体系**：
  - **下行 IRP↓**：USER→VFS→DRV→HW，青蓝色实线，`.connector.active` 高亮
  - **上行 IRQ↑**：HW→INT，中断信号路径
  - **数据返回路径 DATA↑**：DRV→VFS→USER 虚线（钴蓝色），`return-path` 类 + 有数据时 glow 滤镜增强
- **连接器联动**：`phase` 状态机控制三组连线点亮/熄灭，下行阶段仅亮 IRP↓，返回阶段同时点亮 IRQ↑ + DATA↑
- **`IRP ↓` / `IRQ ↑` / `DATA ↑` 三个 `<foreignObject>` 标签**：随阶段切换透明度 (opacity: 1 ↔ 0.3)
- **阶段徽章**：右侧步进控制区动态胶囊徽章（`⏳ 等待初始化` → `⬇ IRP ↓ 下行请求` → `💾 硬件磁盘读取` → `⬆ DATA ↑ 数据返回` → `✅ 完成`），带颜色编码和脉冲动画
- **DMA 控制器面板**：`.dma-grid` 4 格网格（源地址 / 目标地址 / 传输字节 / 状态），状态颜色编码（SETUP 蓝 / TRANSFERRING 橙动画 / DONE 绿 / ERROR 红）
- **页缓存状态徽章**：命中 ✓（绿）/ 未命中（橙）/ 空（灰）/ 已禁用
- **子步骤进度标签**：每层卡片右上角 `.layer-steps`（如 `2/4 步`），仅活动层可见
- **EAGAIN 重试提示**：进程面板内琥珀色重试计数条（仅在 `0 < retryCount < retryMax` 时显示）

**答辩关键词**: SVG、viewBox、绝对定位居中、有向连线动画、阶段可视化、DMA 面板、拓扑可视化

---

## [E] 前端快照渲染与交互控制

**文件**: `web/src/app.js`（555 行）

**负责内容**:
- **`onSnapshot` 快照渲染函数**：将 SystemSnapshot protobuf 映射到 DOM 更新
  - 活动层高亮（`.layer-card.active` + 阶段颜色类 `phase-request` / `phase-return`）
  - 进程状态渲染（RUNNING 绿 / BLOCKED 橙 / READY 灰 徽章切换）
  - 内核缓冲区数据更新（`updateKbuf` — write/read 类切换）
  - 用户缓冲区累加显示（protobuf bytes base64 解码 → TextDecoder 渲染）
  - 硬件寄存器展示（CMD_REG / STS_REG） + DMA 4 寄存器面板
  - 进度条更新（`pbar.fill` + `chunk-stat`）
  - 页缓存状态徽章动态切换（命中 / 未命中 / 空 / 已禁用）
  - 重试状态提示（EAGAIN 重试计数，琥珀色横幅）
  - 错误控制台显示（SUCCESS 绿色 / 错误码红色闪烁动画）
- **阶段状态机（phase）**：`idle → request → hardware → return → complete` 五阶段，通过 `detectPhase(snap, prevSnap)` 根据层流转方向自动检测：DRV→HW 进入 hardware，HW→INT 进入 return，完成进入 complete
- **连接器联动控制**：按 phase 点亮/熄灭对应连线，返回阶段额外点亮 `return-path` 虚线 + DATA↑ 标签，有数据时施加 glow 滤镜
- **Canvas 粒子系统**：贝塞尔曲线轨迹、`getBoundingClientRect` 动态坐标计算（适应窗口缩放）、`requestAnimationFrame` 60fps 动画循环、乒乓切换触发 5 粒子从硬件卡飞向目标缓冲区
- **自动连点三态控制**：「⚡ 自动连点」(播放) →「⏸ 暂停」→「▶ 继续」。6 档速度滑块（10ms~500ms），运行时调速自动重启定时器
- **INIT / RESET 按钮分离**：「● 连接并初始化」新建连接（全新引擎），「↻ 重置 (保留缓存)」复用 stream（继承页缓存）。通过 stream 作用域自然区分，无需后端额外标志位
- **INIT 快照过滤**：跳过来自 INIT 响应的初始快照（subStep===0），不从 stepCount 计数，避免日志中出现 S1 (0/4)
- **手风琴面板折叠动画** + 日志卡片 flex 弹性扩展
- **null 安全**：所有 DOM 操作加 `if (el)` 守卫，防止 SVG foreignObject 查找失败导致整个 onSnapshot 崩溃
- **层级卡片点击跳转** (`detail.html?layer=XX`)

**答辩关键词**: 快照渲染、阶段状态机、DOM 驱动更新、Canvas 粒子动画、贝塞尔曲线、自动连点、智能重置按钮

---

## [F] 前端样式体系与层级详情页

**文件**: `web/src/style.css`（750 行）+ `web/src/detail.html`（381 行）

**负责内容**:
- **CSS 变量设计体系**：颜色令牌（`--phosphor` / `--cobalt` / `--amber` / `--cyan` / `--ink` / `--green`）、背景（`--bg-panel` / `--bg-deep` / `--bg-card`）、边框（`--border` / `--border-light`）、阴影（`--shadow-sm`）
- **6:4 CSS Grid 宽屏主布局** + 右侧面板 flex 纵向弹性链（`flex:1` + `height:0` + `min-height:0` 解决溢出）
- **顶部控制栏**：72px 三区 flex 布局（连接区 / 配置区 / 步进区），header-badge 插入
- **层级卡片样式**：胶囊标签 (L0~L4)、悬停 glow 效果、active/error/phase-request/phase-return 状态切换
- **阶段颜色体系**：phase-request（青蓝边框）、phase-hardware（琥珀边框）、phase-return（钴蓝边框 + 脉冲动画）
- **DMA 面板样式**：`.dma-grid` 2×2 网格、`.dma-status` 状态颜色（dma-setup 蓝 / dma-transferring 橙动画 / dma-done 绿 / dma-error 红）
- **页缓存徽章**：cache-hit（绿）/ cache-miss（橙）/ cache-empty（灰）/ cache-off（muted）
- **连接线样式**：实线 IRP/IRQ + 虚线 return-path（钴蓝） + glow-active 滤镜
- **进度条填充动画** `.pbar-fill`、缓冲区 write/read 色块、错误控制台闪烁动画
- **按钮体系**：btn-primary（蓝）/ btn-reset（红）/ btn-warn（琥珀）/ btn-pause（紫脉冲）/ btn:disabled（灰）
- **自动连点速度滑块**：range input 自定义样式
- **手风琴组件**：`.accordion.open` 控制展开、`.accordion-arrow` 箭头旋转动画
- **详情页 `detail.html`**：
  - 内联 CSS + JS，5:5 双栏布局
  - 根据 `?layer=` URL 参数动态渲染 5 层内容
  - **子步骤列表**严格对应 engine.go 实际 switch case（L0=4, L1=5, L2=3, L4=3, L3=3），故障拦截点标注红色徽章
  - **伪代码**对应 engine.go 实际 Go 代码逻辑
  - **迷你拓扑图**含 SVG 连线 + IRP↓/IRQ↑/DATA↑ 箭头，当前层高亮
  - **数据结构表格**：类型 | 字段名 | 用途说明（如 `PageCache map[string][]byte`）
  - 输入/输出标注（`io-tag` in/out）

**答辩关键词**: CSS Grid/Flexbox、变量体系、阶段颜色编码、DMA 状态动画、detail.html 引擎对应

---

## [G] 部署运维、测试与答辩 PPT

**文件**: `Makefile`、`TEST.md`、`API.md`、云端部署配置

**负责内容**:
- **本地开发环境**：`go run ./cmd/server/` → `localhost:18083`
- **跨平台编译**：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o io-sim-server` 生成无依赖静态二进制
- **测试体系**：
  - Go 自动化测试：`go test ./internal/engine/ -v`（22 个测试场景，覆盖正常流/故障注入/页缓存/DMA/边界）
  - 前端手动测试：`TEST.md`（11 测试大类，TC-01 ~ TC-22，含 5 分钟回归清单）
  - 测试场景覆盖：正常单/双缓冲、6 种故障注入、页缓存命中/未命中/跨 INIT 持久化、DMA 寄存器生命周期、阶段可视化、自动连点速度控制、用户身份切换、进程状态变迁、fd 表与文件偏移量、零字节边界
- **云端部署流程**：
  - SCP 上传二进制 + web 文件 → `chmod +x io-sim-server` → nohup 启动
  - systemd 服务配置（ExecStart / Restart=always）
  - Nginx Proxy Manager 反代 `127.0.0.1:18083` + WebSocket Upgrade（`proxy_http_version 1.1` + `Upgrade $http_upgrade`）
- **API 文档**：`API.md`（gRPC 服务接口、请求/响应格式、错误码表、帧格式说明）
- **项目仓库管理**：Git 工作流、分支策略、commit 规范（feat/fix/docs/refactor）
- **答辩 PPT 制作**：项目背景 → OS 理论（课本对应） → 架构设计 → 技术选型 → 核心亮点（子步骤/DMA/页缓存/EAGAIN/阶段可视化） → Demo 演示流程 → 小组协作
- **演示录制 / 现场 Demo 操作流程**

**答辩关键词**: 跨平台部署、systemd、NPM 反代、22 测试场景、TEST.md、API.md、答辩演示

---

## 模块间依赖关系

```
[A] 引擎核心 ──提供快照──→ [C] Proto 通信层 ──序列化──→ [E] 前端渲染
   │                            │                        │
   └──调用──→ [B] 文件系统校验    └──生成──→ pb 代码        ├──渲染──→ [D] SVG 拓扑
                                                         │          │
                                                         └──样式──→ [F] CSS + detail.html
                                                                    │
                                              [G] 测试 + 部署 + 文档 ←┘
```

## 认领说明

1. 每人在群里报两个志愿（第一志愿 + 第二志愿），组长根据志愿分配
2. 认领后请阅读对应代码文件，不理解的地方随时问组长
3. 答辩时每个人需要能讲清楚自己模块的**设计思路**和**与 OS 课本知识的对应关系**
4. 模块 C（通信层）和 A（引擎核心）难度较高，建议有 Go/网络编程基础的同学选。模块 E 代码量最大（555 行）但逻辑清晰
5. 模块 F 和 G 难度较低但答辩需要展示视觉/文档成果

---

## 组长已完成的工作

| 类别 | 内容 |
|------|------|
| 项目架构 | 前后端分离 + gRPC-Web 双向流 + 五层子步骤状态机的整体设计 |
| Go 后端 | `cmd/server/`、`internal/engine/engine.go`（644 行，18 子步骤）、`internal/engine/filesystem.go`（132 行）、`internal/service/handler.go`（102 行）全部代码 |
| Proto 定义 | `api/proto/io_simulation.proto`（9 message + 6 FaultType）+ `make proto` 代码生成 |
| 前端 | `index.html`、`style.css`、`app.js`（555 行）、`grpc-entry.js`（197 行）、`detail.html`（295 行）全部代码 |
| 测试 | `engine_test.go`（652 行，22 场景）+ `TEST.md`（22 测试用例 + 5 分钟回归清单）|
| 部署 | 跨平台编译 + 云端部署流程 + systemd + NPM 配置 |
| 文档 | `README.md` + `DESIGN.md` + `API.md` + `TEST.md` + `TEAM.md` |
| Git | 仓库管理 + 全部 commit |
