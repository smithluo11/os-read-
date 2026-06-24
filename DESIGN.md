# OS I/O 模拟器 — 设计文档

> 操作系统课程设计 · 选题七：I/O 软件层 read 操作模拟 · 8 人小组

---

## 一、项目概述

本项目严格对标《操作系统课程设计》**选题七：I/O 软件层 read 操作模拟**，通过浏览器内可交互的 SVG 拓扑动画 + Go 后端状态机引擎，完整演示一次 `sys_read(fd, buf, len)` 系统调用从用户空间到磁盘控制器的五层全链路流转。支持单步执行、故障注入、双缓冲乒乓切换、数据返回路径可视化，以及层级详情页的逐层剖析。

---

## 二、I/O 系统理论基础（结合课本）

### 2.1 I/O 软件分层架构

课本将 Linux I/O 软件栈划分为四层，本项目额外加入硬件层使流程完整：

```
L0  用户层 I/O 软件          sys_read(fd, buf, len) — 发起 read 请求
      │  IRP ↓                                        (系统调用陷入内核)
L1  设备无关软件 (VFS)       路径解析 → 权限校验 → 缓冲分配 → 构造 IRP
      │  IRP ↓
L2  设备驱动程序             写入设备控制寄存器 → 下发 DMA 指令 → 进程阻塞
      │  IRP ↓
L4  硬件层 (磁盘控制器)       磁盘寻道 → 扇区读取 → DMA 传输 → 发 IRQ
      │  IRQ ↑
L3  中断处理程序 (ISR)       ISR 响应 → 数据拷贝至内核缓冲区 → copy_to_user
      │  DATA ↑                                        → 唤醒进程 → 逐层返回
L0  用户层 (数据返回)         用户进程继续执行，read() 返回字节数
```

> 课设要求的四层软件层（用户层 I/O → 设备无关软件 → 设备驱动 → 中断处理）全部覆盖，硬件层为扩展。

### 2.2 CPU 与设备控制器的通信方式：内存映像 I/O

课本介绍了两种 CPU 访问 I/O 设备的方式：

| 方式 | 原理 | 本项目采用 |
|------|------|-----------|
| **端口映像 I/O（Port-Mapped I/O）** | CPU 通过独立的 I/O 地址空间（`in`/`out` 指令）访问设备寄存器 | |
| **内存映像 I/O（Memory-Mapped I/O）** | 设备寄存器映射到内存地址空间，CPU 直接用 `load`/`store` 指令访问 | 是 |

本项目硬件层设计了三个设备寄存器 —— `CMD_REG`（命令寄存器）、`STS_REG`（状态寄存器）、`DATA_REG`（数据寄存器），驱动程序通过写入这些寄存器来控制磁盘控制器。这正是内存映像 I/O 的典型模型，与课本图 5-7 描述一致：

- 驱动层写 `CMD_REG = 0x01: READ_SECTOR` 发出读盘命令
- 驱动层读 `STS_REG = 0x02: DEVICE_BUSY` 确认设备忙
- 硬件完成后设 `STS_REG = 0x04: DATA_READY`，数据载入 `DATA_REG`
- ISR 从 `DATA_REG` 取走数据并清零，设 `STS_REG = 0x01: READY`

### 2.3 I/O 控制方式：中断驱动 DMA

课本给出了 I/O 控制方式的演进路线：

```
程序直接控制 (轮询) → 中断驱动 I/O → DMA 控制器 → I/O 通道处理器
```

本项目采用的是 **中断驱动 DMA** 模型，而非轮询（Polling）。核心流程：

1. **CPU 编程 DMA 控制器**：驱动层将源（磁盘扇区）/目标（内核缓冲区地址）/长度写入 DMA 控制器参数，然后阻塞进程
2. **DMA 自主传输**：DMA 控制器接管总线，独立完成磁盘 → 内存的数据搬运，不占用 CPU
3. **中断通知 CPU**：DMA 传输完成后，硬件向 CPU 发出中断信号（IRQ），将 `STS_REG` 设为 `DATA_READY`
4. **ISR 响应**：中断处理程序唤醒，将数据从内核缓冲区拷贝到用户空间（`copy_to_user`），恢复进程为 RUNNING

代码层面，`engine.go` 完整实现了这个模型：

```
引擎 executeDriverLayer() (line 170-195):
  → 进程状态 = BLOCKED, 等待原因 = "等待物理硬件 I/O 中断信号"
  → 硬件 CMD_REG = "READ_SECTOR (DMA→BufN)"

引擎 executeHardwareLayer() (line 197-237):
  → 模拟磁盘寻道 → 数据载入 DATA_REG
  → STS_REG = DATA_READY
  → "正在进行 DMA 传输至内核缓冲区。向 CPU 发出硬件中断信号！"

引擎 executeInterruptLayer() (line 239-319):
  → ISR 将硬件数据寄存器内容拷贝至内核缓冲区 (DMA 完成)
  → copy_to_user 累加到用户缓冲区
  → 进程状态恢复为 RUNNING
```

### 2.4 为什么不使用「通道」而选择「双缓冲」

#### 通道（I/O Channel）的概念

I/O 通道是比 DMA 控制器更高阶的 I/O 管理单元。通道本身是一台**专用处理器**，拥有自己的指令集（通道命令字，CCW），能够执行一段完整的通道程序，管理多台设备的并发 I/O 操作。通道的出现是为了解决大规模 I/O 负载下的 CPU 干预开销问题。

课本中通道的主要特征：

- 通道有自己的指令集（READ、WRITE、CONTROL、SENSE 等 CCW）
- 通道可以同时管理多台设备控制器
- 通道通过 CAW（通道地址字）/ CSW（通道状态字）与 CPU 通信
- 通道程序以 CCW 链形式组织，通道自行取指、译码、执行

#### 为什么本项目不引入通道

通道是大型机/高 I/O 吞吐场景（如银行交易处理、数据库集群）才采用的架构。本项目的教学目标是可视化学懂 `sys_read()` 的一次完整调用，核心在于：

1. 展示 I/O 软件分层结构
2. 展示中断驱动机制
3. 展示缓冲技术的作用

引入通道会让状态机复杂度急剧增加（需要模拟 CCW 取指/译码/执行循环、多设备调度、通道与 CPU 的握手协议），但教学的增量收益很小。因此在设计阶段，我们**参照了课本中通道「减少 CPU 干预、并行数据传输」的设计思想**，选择了更适合教学场景的方案：**双缓冲（乒乓缓冲）**。

#### 双缓冲的课本地位

双缓冲（Double Buffering / Ping-Pong Buffer）在课本 I/O 软件层章节作为「缓冲技术」的核心内容出现。它的思想与通道殊途同归：**让 I/O 操作与 CPU 处理并行执行**。

| | 通道（Channel） | 双缓冲（Double Buffering） |
|---|---|---|
| **并行层面** | 通道程序独立于 CPU 执行，管理多设备 | 两块内核缓冲区交替读写，DMA 写一块时 CPU 读另一块 |
| **复杂度** | 高（需要 CCW 程序、通道状态机、多设备仲裁） | 中（状态机可直观建模） |
| **教学价值** | 偏硬件体系结构 | 偏操作系统软件层，更贴合选题七范围 |
| **可视化** | 难以在浏览器中直观呈现 | 缓冲 A/B 乒乓切换可通过颜色高亮 + 粒子动画直观展示 |

**结论**：本项目以中断驱动 DMA 为 I/O 控制方式，以双缓冲为缓冲策略。双缓冲的乒乓切换机制充分体现了「I/O 与计算重叠执行」这一核心思想，与通道的设计目标一脉相承，但在实现复杂度和教学效果之间取得了更好的平衡。

---

## 三、系统架构

```
┌──────────────────────────────────────────────────────┐
│  浏览器前端 (HTML5 + CSS3 + Vanilla JS)                │
│  ├─ SVG 五层拓扑图 + 6 条有向连线 (IRP↓/IRQ↑/DATA↑)    │
│  ├─ Canvas 粒子系统 (贝塞尔曲线动态数据流动画)         │
│  ├─ 手风琴信息面板 (进程/内存/硬件/差错控制台/日志)     │
│  ├─ 层级详情页 detail.html (6:4 布局, 深层剖析)        │
│  ├─ 手写 gRPC-Web WebSocket 帧编解码器                 │
│  └─ google-protobuf JS runtime                        │
└────────────┬─────────────────────────────────────────┘
             │ WebSocket (grpc-websockets, bidi streaming)
┌────────────▼─────────────────────────────────────────┐
│  Go 后端 (cmd/server/main.go)                         │
│  ├─ grpcweb.WrapServer — gRPC-Web WS 帧包装           │
│  ├─ internal/service/handler.go — 双向流处理           │
│  ├─ internal/engine/engine.go — 核心状态机引擎         │
│  │   ├─ 五层递进推进 (NextStep)                       │
│  │   ├─ 双缓冲乒乓切换控制器 (chunk 分块, A/B 交换)    │
│  │   └─ 6 种故障注入 (EACCES/EFAULT/EIO/EPERM/ENOENT/EAGAIN) │
│  ├─ internal/engine/filesystem.go — ACL 文件系统校验   │
│  │   ├─ 路径穿越攻击检测 (.. 模式防御)                │
│  │   ├─ Unix 权限位检查 (owner/group/other 9 位)      │
│  │   └─ 敏感文件 root-only 保护                       │
│  └─ http.FileServer — 静态文件服务 (STATIC_DIR)        │
└──────────────────────────────────────────────────────┘
```

### 技术栈

| 层 | 技术选型 | 说明 |
|---|---------|------|
| 前端框架 | Vanilla JS | 无框架依赖，直接操作 DOM |
| UI 布局 | CSS Grid 6:4 宽屏排版 + Flexbox | CSS 变量体系，浅色实验室主题 |
| 图形 | SVG (拓扑连线) + Canvas (粒子动画) | 动态 getBoundingClientRect 坐标计算 |
| 通信协议 | gRPC-Web (Improbable) over WebSocket | 双向流 (bidi streaming) |
| 前端 pb | google-protobuf JS runtime | SimControlCommand / SystemSnapshot 编解码 |
| 后端语言 | Go | 交叉编译单二进制部署 |
| 后端 pb | protoc + protoc-gen-go | 由 Makefile 自动生成 |
| 部署 | 二进制 + systemd + Nginx Proxy Manager | 反代 127.0.0.1:18083 + WebSocket Upgrade |

---

## 四、核心模块实现细节

### 4.1 状态机引擎（`internal/engine/engine.go`）

引擎采用 **层 + 子步骤** 双层状态机架构。每次 `NextStep()` 调用推进一个子步骤 (`SubStep`)，而非直接切换层。五个层共计 18 个子步骤（L0=4, L1=5, L2=3, L4=3, L3=3），单缓冲完整流程 18 步，双缓冲 (4 chunk) 约 42 步。

```
L0 用户层 I/O 软件 (4 子步骤)
  [1/4] 库函数调用: fread() → libc 从 FILE* 提取 fd
  [2/4] fd 表查找: 遍历 FdTable, fd=3 → OpenFileEntry{path, flags, pos, inode}
        故障: fd 不存在 → EBADF
  [3/4] access_ok(): 校验 buf 地址 < TASK_SIZE (0xC0000000)
        故障: 地址越界 + FAULT_INVALID_ADDRESS → EFAULT
  [4/4] 陷入内核: eax=NR_read, int 0x80 / sysenter, Ring3→Ring0

L1 设备无关软件 VFS (5 子步骤)
  [1/5] 路径解析: path → dentry → inode (dcache 查找)
  [2/5] ACL 权限校验: 4 级管线 (路径穿越→文件存在→敏感文件→Unix 权限位)
        故障: EACCES / EPERM / ENOENT 即时终止
  [3/5] 页缓存查找: 命中 → 直接返回数据 (跳过 L2/L4/L3); 未命中 → 继续磁盘 I/O
  [4/5] 缓冲分配: 单缓冲 vs 双缓冲 (ping-pong A/B, TotalChunks 计算)
  [5/5] IRP 构造: IRP_READ → Dev:Disk0, 向驱动层提交

L2 设备驱动程序 (3 子步骤)
  [1/3] IRP 解析: 提取操作类型、设备、长度、数据块序号
  [2/3] 寄存器编程: 内存映射 I/O 写入 CMD_REG (READ_SECTOR), STS_REG (DEVICE_BUSY)
  [3/3] 进程阻塞: STATE_BLOCKED, WaitReason="等待 DMA 传输 + 硬件中断"

L4 硬件层 磁盘控制器 (3 子步骤)
  [1/3] DMA 启动: 初始化源地址(磁盘扇区)→目标地址(内核缓冲区), DMA 接管总线
  [2/3] 磁盘读取: 磁头寻道→扇区数据→DATA_REG, 故障: FAULT_HARDWARE_TIMEOUT → EIO
  [3/3] 硬件中断: STS_REG=DATA_READY, IRQ#14 → CPU

L3 中断处理程序 ISR (3 子步骤)
  [1/3] ISR 接管: 硬件自动压栈 EIP/CS/EFLAGS, 保存寄存器上下文
  [2/3] 数据拷贝: HW DATA_REG → 内核缓冲区 → copy_to_user() → 用户缓冲区 (累加)
  [3/3] 乒乓切换/I/O 完成:
        ├─ 更多 chunk → A↔B 翻转, 编程下次 DMA, 进程阻塞 → 回 L2 (循环)
        └─ 最后 chunk → 进程 RUNNING, 更新文件偏移量, IsFinished=true
```

#### L0 用户层数据结构

```go
type OpenFileEntry struct {
    Fd       int32   // 文件描述符 (如 3)
    FilePath string  // 关联的文件路径
    Flags    int32   // 打开标志: 0=O_RDONLY
    FilePos  int64   // 当前文件读写偏移量
    InodePtr string  // 指向 inode 的指针标识
}
```

`SimulationEngine.FdTable` 维护进程的打开文件表，在 L0 sub-step 2 中实际执行 fd→file*→inode 的查找。I/O 完成后 `FilePos` 更新，模拟 lseek 偏移量推进。

#### 双缓冲（乒乓缓冲）核心算法

```
初始化: ActiveWriteBuffer = 1, ActiveReadBuffer = 1, CurrentChunk = 0
TotalChunks = ceil(BytesToRead / 4096)

每次 ISR 响应后 (sub-step 3):
  if CurrentChunk + 1 < TotalChunks:
      CurrentChunk++
      ActiveReadBuffer  = ActiveWriteBuffer    // 刚写入的变读取源
      ActiveWriteBuffer = 3 - ActiveWriteBuffer // 1↔2 翻转 (乒乓)
      编程下次 DMA → 硬件开始写新 ActiveWriteBuffer
      进程 BLOCKED → 回到 Driver 层循环
  else:
      进程 RUNNING，更新 FilePos += bytesRead，模拟完成
```

前端通过粒子动画可视化乒乓切换：当 `activeWriteBuffer` 变更时，Canvas 粒子从硬件卡片以贝塞尔曲线飞向新的目标缓冲区。

### 4.2 文件系统校验管线（`internal/engine/filesystem.go`）

实现了一条完整的 ACL 安全校验链，模拟 Linux 内核 VFS 层的权限检查逻辑：

```
路径穿越检测 → 文件存在检查 → 敏感文件检查 → Unix 权限位检查
```

**路径穿越防御**：解析路径中的 `..` 段为规范绝对路径，判断是否逃逸出用户主目录范围。例如 `../../etc/shadow` 解析后为 `/etc/shadow`，不在 `/home/user1/` 下，判定为路径穿越攻击，返回 `EFAULT`。

**Unix 权限位检查**：遵循 Linux 9 位权限模型（owner/group/other × rwx），按 `uid==0`（root 绕过）→ `uid==owner`（owner read bit）→ `gid==group`（group read bit）→ other read bit 的顺序判定。

### 4.2.1 页缓存 (Page Cache) — VFS 层加速

模拟 Linux 内核的页缓存 (page cache / buffer cache) 机制。`SimulationEngine` 维护 `PageCache map[string][]byte`，以文件路径为键缓存已读取的数据。

```
L1 sub-step 3: 页缓存查找
  ├─ 命中 (Cache Hit) → 数据直接从 RAM 拷贝到用户缓冲区
  │   跳过 L2/L4/L3 全部磁盘 I/O，立即返回 IsFinished=true
  │
  └─ 未命中 (Cache Miss) → 设置 CacheMissed=true，继续走磁盘 I/O
      读取完成后在 L3 sub-step 3 回写 PageCache
```

前端通过页缓存状态徽章实时显示：命中 ✓（绿）、未命中（橙）、空（灰）、已禁用。

### 4.2.2 EAGAIN 可恢复错误 — 非终态重试

区别于 5 种终态故障（一旦触发立即终止），EAGAIN 模拟设备控制器暂时忙碌的可恢复场景。在 L2 驱动层 sub-step 2（寄存器编程）中触发：

```
驱动尝试写 CMD_REG → 设备返回 DEVICE_BUSY (EAGAIN)
  ├─ RetryCount < RetryMax(3) → NextStep 通过 retrySubStep 标志跳过 SubStep 推进
  │   停留在 sub-step 2，点击「下一步」继续重试
  └─ RetryCount >= RetryMax → 重试成功，正常编程寄存器，继续 I/O
```

`SimulationEngine` 维护 `RetryCount`、`RetryMax` 和 `retrySubStep` 布尔标志。`MemoryView` 含 `retry_count`/`retry_max` 字段供前端显示。`retrySubStep` 作为显式控制标志（而非直接操作 `SubStep` 数值），使 NextStep 的重试跳过逻辑自文档化。

### 4.3 gRPC-Web 双向流通信

```
┌─ 前端 grpc-entry.js ──────────────────────────────┐
│                                                    │
│  SimControlCommand.serializeBinary()               │
│         ↓                                          │
│  frameMessage(): [control=0][flag=0][4B BE len][pb]│
│         ↓  WebSocket.send()                        │
│         ↓  ws.binaryType = 'arraybuffer'           │
│                                                    │
│  WebSocket.onmessage → parseFrame():               │
│    读取 [flag][4B BE len][data]                    │
│    → SystemSnapshot.deserializeBinary(data)        │
│    → onSnapshot(snap) 回调                         │
└────────────────────────────────────────────────────┘
```

前端 `grpc-entry.js` 手写了 Improbable gRPC-Web WebSocket 帧编解码器。Improbable 协议的帧格式为：

```
Byte 0:     WebSocket transport control (0 = data frame)
Byte 1:     gRPC frame flag (0 = data, 128 = trailer)
Byte 2-5:   4 字节大端序 payload 长度
Byte 6+:    protobuf 序列化数据
```

这一层是项目最底层的通信基础设施，197 行代码，零第三方依赖。

### 4.4 前端可视化

#### SVG 拓扑连线与子步骤指示

五层拓扑图使用 SVG `<line>` 元素连接各层卡片，所有坐标通过 `viewBox="0 0 720 750"` 统一管理。包括：

- **下行路径** (IRP↓)：USER→VFS→DRV→HW，青蓝色实线高亮
- **上行路径** (IRQ↑)：HW→INT→DRV，青蓝色实线
- **数据返回路径** (DATA↑)：DRV→VFS→USER，钴蓝色虚线 + DATA↑ 标签

每层卡片右上角显示**子步骤进度标签**（如 `2/4 步`），橙色高亮，仅活动层可见。日志格式 `[S2 (2/4)]` 标记当前子步骤位置。

#### 下行/上行阶段独立展示

前端 `onSnapshot()` 内根据层流转方向自动判断当前阶段：

```
idle → request (IRP↓ 下行) → hardware (磁盘读取) → return (DATA↑ 返回) → complete
```

- 顶部胶囊标签动态切换：青蓝色「IRP ↓ 下行请求」→ 钴蓝色「DATA ↑ 数据返回」
- 活动层卡片边框颜色随阶段变化（下行青蓝 / 上行钴蓝）
- SVG 连接器按阶段点亮：下行只亮 IRP↓ 路径，上行点亮 IRQ↑ + DATA↑ 路径

#### DMA 控制器面板

硬件层卡片下方渲染 DMA 控制器 4 寄存器网格：

| 寄存器 | 说明 | 值示例 |
|--------|------|--------|
| 源地址 | 磁盘扇区 | `0x0400: Disk0 Sector` |
| 目标地址 | 内核缓冲区 | `0xBEEF1000: Kernel Buf1` |
| 传输字节 | 当前 DMA 传输量 | `4096` |
| DMA 状态 | 传输阶段 | `IDLE → SETUP → TRANSFERRING → DONE/ERROR` |

状态颜色编码：SETUP(蓝) → TRANSFERRING(橙动画) → DONE(绿) → ERROR(红)。

#### 自动连点速度控制与初始化/重置分离

- **速度控制**：`auto-speed` 滑块 0-5 档映射 10/25/50/100/200/500ms，实时显示当前间隔。运行时调整速度自动重启定时器
- **三态按钮**：「⚡ 自动连点」(播放) →「⏸ 暂停」→「▶ 继续」
- **INIT / RESET 按钮分离**：
  - 「● 连接并初始化」— 新建 WebSocket 连接 + INIT，后端 simEngine 为 nil → 全新引擎（不含缓存）
  - 「↻ 重置 (保留缓存)」— 复用当前 stream 发送 INIT，后端 simEngine 仍在 → 自动继承旧页缓存
  - 两个按钮通过 stream 作用域自然区分，无需后端额外标志位
- 自动连点按钮和速度滑块始终可见（不再需要 `?debug=1`）
- 阶段徽章（phase-badge）位于右侧步进控制区，与「下一步」/「自动连点」同一分组

前端 JS 通过 `getBoundingClientRect()` 实时获取卡片在 Canvas 上的位置，而非依赖硬编码坐标，使得页面缩放时粒子动画仍然准确。

#### Canvas 粒子系统

当双缓冲发生乒乓切换时，Canvas 生成 5 个粒子，沿二次贝塞尔曲线从硬件层卡片飞向目标缓冲区卡片。核心代码：

```javascript
// 贝塞尔曲线参数方程
const mt = 1 - t;
const x = mt*mt*sx + 2*mt*t*mx + t*t*ex;
const y = mt*mt*sy + 2*mt*t*my + t*t*ey;
// 淡入淡出透明度
const alpha = t < 0.15 ? t/0.15 : t > 0.85 ? (1-t)/0.15 : 1;
```

粒子起点从硬件卡上方发出，控制点向右偏移 50px 形成弧线轨迹，`requestAnimationFrame` 驱动 60fps 循环渲染。

#### 层级详情页

`detail.html?layer=XX` 根据 URL 参数动态渲染 5 层内容。每层包含：

- **输入/输出**：标注每层接收的数据格式和产出
- **关键数据结构**：inode、file、IRP、DMA 描述符、中断向量表等
- **操作步骤**：各层按实际子步骤数展示（L0=4, L1=5, L2=3, L4=3, L3=3）
- **伪代码**：带语法高亮的类 C 伪代码

---

## 五、测试方法

### 5.1 正常流测试

| 测试场景 | 配置 | 预期结果 |
|---------|------|---------|
| 单缓冲 + 普通文件 | file=/home/user1/notes.txt, dblbuf=off, user=user1 | 18 步子步骤完成，L0 4 步 (fd 表→access_ok→陷入内核)，I/O 完成后显示文件偏移量更新 |
| 双缓冲 + 普通文件 | file=/home/user1/notes.txt, dblbuf=on, user=user1 | 约 42 步 (4KB×4 chunk)，乒乓切换可见，粒子动画触发，层卡片显示子步骤进度 |
| 双缓冲 + 大文件 (65536B) | bytes=65536, dblbuf=on | 16 个 4KB chunk 完整传输 (约 138 步)，缓冲区 A/B 多次翻转 |

### 5.2 故障注入测试

| 故障类型 | 注入方式 | 错误码 | 验证点 |
|---------|---------|--------|--------|
| 权限拒绝 | 故障下拉选 EACCES | EACCES | L1 sub-step 2 (ACL) 中断，卡片变红，差错控制台红色 |
| 非法地址 | 选 0xFFFFFFFF 地址 | EFAULT | L0 sub-step 3 (access_ok) 中断，地址越界提示 |
| 硬件超时 | 故障下拉选 EIO | EIO | L4 sub-step 2 中断，STS=DEVICE_ERROR，DMA 状态=ERROR |
| 路径穿越 | 故障下拉选 EPERM | EPERM | L1 sub-step 2 (路径穿越检测) 中断 |
| 文件不存在 | 故障下拉选 ENOENT | ENOENT | L1 sub-step 2 (文件存在检查) 中断 |
| 设备忙重试 | 故障下拉选 EAGAIN | EAGAIN | L2 sub-step 2 触发，前端显示重试计数 1/3→2/3→3/3，然后成功 |

### 5.3 页缓存测试

| 测试场景 | 配置 | 预期结果 |
|---------|------|---------|
| 首次读取 (缓存未命中) | 页缓存开启 + 正常流 | VFS sub-step 3 显示「未命中」，走完整磁盘 I/O，完成后回写缓存 |
| 二次读取 (缓存命中) | 同上配置，再点一次初始化 | VFS sub-step 3 显示「命中 ✓」，直接返回数据，跳过 L2/L4/L3 |

### 5.4 前端新增功能测试

1. **阶段标签**：走完一次完整流程，验证顶部胶囊从「等待初始化」→「IRP ↓ 下行请求」→「💾 硬件磁盘读取」→「DATA ↑ 数据返回」→「✅ 完成」
2. **DMA 面板**：硬件层激活时 DMA 4 寄存器可见，状态依次变化 SETUP→TRANSFERRING→DONE
3. **速度控制**：自动连点 → 调整速度滑块 → 暂停 → 继续
4. **重置按钮**：模拟完成后按钮「↻ 重置 (保留缓存)」出现 → 点击复用当前 stream 重新初始化（页缓存继承），INIT 按钮单独点击则新建连接（全新引擎）

### 5.5 权限边界测试

| 测试条件 | 结果 |
|---------|------|
| Root (uid=0) 读 /etc/shadow (敏感文件) | 通过（root 绕过所有权限检查） |
| user1 (uid=1000) 读 /etc/shadow | 拒绝（敏感文件仅 root 可读 + owner/group 权限位拒绝） |
| user1 (uid=1000) 读 /home/user1/notes.txt (owner 文件) | 通过（owner read bit=4, 0644 允许） |
| user1 (uid=1000) 读 /tmp/data.bin (other 可读) | 通过（other read bit=4, 0666 允许） |

### 5.6 前端集成测试

1. **连接状态机联动**：点击「连接并初始化」→ 状态灯变绿 → 按钮自动启用
2. **单步 / 自动连点**：逐步点击验证每层卡片高亮正确；自动连点 50ms 间隔无卡顿
3. **手风琴面板**：折叠/展开不破坏 flex 链式布局
4. **层级详情页跳转**：点击每个层卡片 → 跳转正确层级的 detail.html → 点返回按钮回到主页
5. **数据返回路径**：双缓冲完成时 DATA↑ 虚线点亮，IRP↓ 和 IRQ↑ 联动切换
6. **粒子动画**：放大/缩小页面后播放粒子动画，起终点无偏移

---

## 六、难点与解决方案

### 6.1 gRPC-Web WebSocket 帧格式 —— 手写编解码器

**问题**：Improbable gRPC-Web 的 WebSocket 传输协议在 npm 上需要用 `grpc-web` 包，但这会引入大量 npm 依赖（约 2 万行代码），与项目「零依赖前端」的理念冲突。更重要的是，官方包中的 WebSocket 帧格式文档稀少，不同版本的帧格式存在差异。

**解决方案**：逆向 Improbable 的 `grpc-websocket-proxy` 源码，手写帧编解码器（`grpc-entry.js`）。

帧结构：
```
[0] [flag] [len_BE_4B] [...protobuf payload...]
```

- Byte 0：WebSocket transport control byte（`0` = 数据帧）
- Byte 1：gRPC 帧 flag（`0` = 数据，`128` = trailer/grpc-status）
- Byte 2-5：4 字节大端序 uint32 payload 长度
- Byte 6+：protobuf 序列化二进制

同时需要处理：WebSocket 首帧 ASCII header 块（`Content-Type: application/grpc-web+proto`）、消息队列（在 WebSocket 握手完成前缓存）、粘包/半包缓冲（`parserBuf` 累加 + 逐帧解析）。

**价值**：197 行代码替代了 2 万行的 npm 依赖，且对整个通信路径有了完全的控制权。

### 6.2 Canvas 粒子动画坐标系统

**问题**：SVG 拓扑图使用 `viewBox` 虚拟坐标系，而 Canvas 使用像素坐标系。层级卡片是 `position: absolute` 放置在 SVG 上的 HTML DIV 元素。当浏览器窗口缩放、页面滚动或 CSS transform 变动时，粒子的起终点会与目标卡片错位。

**解决方案**：不依赖任何硬编码坐标。所有粒子的起终点通过 `getBoundingClientRect()` 在运行时动态计算：

```javascript
function getElementCenter(el) {
    const rect = el.getBoundingClientRect();
    const canvasRect = canvas.getBoundingClientRect();
    return {
        x: rect.left - canvasRect.left + rect.width / 2,
        y: rect.top - canvasRect.top + rect.height / 2
    };
}
```

每次动画触发前调用 `resizeCanvas()` 将 Canvas 尺寸同步到父容器的像素尺寸，确保两者坐标系一致。

### 6.3 CSS 多层布局 —— 三栏比例与垂直伸缩

**问题**：主页采用 6:4 的 CSS Grid 布局，右侧信息面板需要同时容纳 4 个手风琴卡片：进程状态、用户缓冲区、错误控制台、系统调用日志。日志卡片需要在展开时占据剩余垂直空间，折叠时收缩，同时不能撑破父容器。

**解决方案**：使用 `flex: 1` + `min-height: 0` + `height: 0` 的 flex 弹性链：

```css
.panel-info { display: flex; flex-direction: column; }
.log-card { flex: 1; overflow: hidden; }
.log-card .log-scroll { flex: 1; height: 0; overflow-y: auto; }
```

`height: 0` 是关键技巧 —— 在 flex 容器中子元素的 `flex: 1` 的初始主轴尺寸由 `flex-basis`（默认 `auto`）决定。当内容过长时，`auto` 会导致元素撑大。显式设 `height: 0` 强制从零开始分配剩余空间。折叠状态通过 JS 动态切换 `flex` 值。

### 6.4 层级卡片居中定位

**问题**：五层卡片需要在 SVG 拓扑图中以 x=360 为中心绝对居中，且卡片宽度各异（USER=500px, VFS=560px, DRV=500px, HW/INT=330px）。

**解决方案**：`left: 50%; transform: translateX(-50%)` 组合 —— CSS 的百分比 `left` 相对于包含块宽度，`translateX(-50%)` 相对于元素自身宽度。无论卡片多宽，始终精确居中，且不受 `position: absolute` 和父元素 `position: relative` 影响。

### 6.5 跨平台部署 —— 单二进制 + NPM

**问题**：云端部署需要在 Alibaba Cloud Linux 服务器上运行后端 + 提供前端页面，但不能在服务器上安装 Go 工具链或 npm。

**解决方案**：
1. 本地交叉编译：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o io-sim-server`，生成无 CGO 依赖的静态链接二进制
2. Go 内嵌静态文件服务：通过 `http.FileServer` + `STATIC_DIR` 环境变量指向 `web/src/`
3. Nginx Proxy Manager 反代 `127.0.0.1:18083`，开启 WebSocket Upgrade 支持
4. systemd 服务保活：`ExecStart=/home/admin/Os/io-sim-server`，崩溃自动重启

### 6.6 Protobuf 流式协议的状态同步

**问题**：gRPC 双向流模式下，前端发送 `SimControlCommand`，后端返回 `SystemSnapshot`。如果后端初始化后没有立即发送初始快照，前端将处于无状态；如果快照更新不完整（如缺少 MemoryState 中的某个字段），前端渲染会报错。

**解决方案**：后端在 `ACTION_INIT` 时立即 `stream.Send(simEngine.Snapshot)` 推送包含完整初始状态的快照。每个 `ACTION_STEP_NEXT` 后同样完整推送。`NewEngine()` 函数确保 `Snapshot` 的所有嵌套 message（ProcessBlock、MemoryView、HardwareView）都有非 nil 的初始值，前端无需做 defensive null check。

### 6.7 前端动画与状态同步的时序

**问题**：双缓冲乒乓切换触发粒子动画时，需要确保 Canvas 已 resize 到当前尺寸，且目标 DOM 元素的位置已随布局更新完成。

**解决方案**：粒子动画在 `onSnapshot` 回调中触发（而非独立定时器），这保证了粒子生成时 DOM 状态与快照一致。`spawnParticles()` 内部首先调用 `resizeCanvas()` 同步尺寸，然后通过 `getBoundingClientRect()` 抓取最新位置，消除时序竞态。

---

## 七、部署架构

```
用户浏览器
    │
    ▼
Nginx Proxy Manager (443/TLS)
    │ proxy_pass http://127.0.0.1:18083
    │ proxy_http_version 1.1
    │ proxy_set_header Upgrade $http_upgrade
    │ proxy_set_header Connection "upgrade"
    ▼
io-sim-server (systemd, 127.0.0.1:18083)
    ├─ /io_simulator.IOSimulationEngine/StreamSimulation → gRPC-Web WS handler
    └─ / → http.FileServer(STATIC_DIR=web/src/)
```

---

## 八、项目结构

```
.
├── api/
│   ├── proto/io_simulation.proto   # Protobuf 定义 (9 个 message, 1 个 service, 6 种 FaultType)
│   └── pb/                         # protoc 自动生成 (禁止手改)
├── cmd/server/main.go              # 服务入口 + 静态文件服务
├── internal/
│   ├── engine/
│   │   ├── engine.go               # 状态机引擎 (644 行, 子步骤 + 页缓存 + EAGAIN + DMA)
│   │   ├── engine_test.go          # 引擎测试 (652 行, 22 个场景)
│   │   └── filesystem.go           # ACL 文件校验管线 (132 行, 4 级校验)
│   └── service/handler.go          # gRPC 双向流处理 (110 行, 含跨 INIT 页缓存持久化)
├── web/src/
│   ├── index.html                  # 主页: 五层拓扑图 + DMA 面板 + 阶段标签
│   ├── detail.html                 # 层级详情页: 逐层剖析
│   ├── style.css                   # 全局样式表 (CSS 变量 + 6:4 Grid + 阶段/缓存/DMA)
│   ├── app.js                      # 前端逻辑 (快照渲染 + 阶段切换 + 粒子动画 + 自动连点)
│   ├── grpc-entry.js               # gRPC-Web WS 客户端 (手写帧编解码)
│   └── bundle.js                   # google-protobuf + pb 打包
├── Makefile                        # proto / proto-js / run 快捷命令
├── DESIGN.md                       # 本设计文档
├── API.md                          # gRPC API 参考文档
└── README.md                       # 项目文档 (快速开始 + 小组分工)
```

---

## 九、小组分工（7 模块）

> 详见 [TEAM.md](./TEAM.md) — 每个模块的详细职责、涉及文件、答辩关键词和模块间依赖关系。

| 编号 | 模块 | 难度 | 核心文件 |
|------|------|------|---------|
| [A] | 状态机引擎核心 | ★★★★ | `engine.go` (644 行, 18 子步骤, 6 故障注入, 页缓存, EAGAIN, DMA) |
| [B] | 文件系统校验与 ACL | ★★★ | `filesystem.go` (132 行, 4 级安全管线) |
| [C] | Proto 协议与 gRPC-Web 通信 | ★★★★ | `io_simulation.proto` + `handler.go` + `grpc-entry.js` |
| [D] | 前端拓扑图与 SVG 连线动画 | ★★★ | `index.html` SVG 五层拓扑 + 阶段可视化 + DMA 面板 |
| [E] | 前端快照渲染与交互控制 | ★★★ | `app.js` (555 行, 阶段状态机 + 粒子动画 + 自动连点) |
| [F] | 前端样式体系与层级详情页 | ★★ | `style.css` (750 行) + `detail.html` (381 行) |
| [G] | 部署运维、测试与答辩 PPT | ★★ | Makefile + TEST.md + API.md + 云端部署 |

---

## 十、参考资料

- Tanenbaum & Bos, *Modern Operating Systems*, 4th Edition, Chapter 5: Input/Output
- 操作系统课程设计实验指导书 — 选题七: I/O 软件层 read 操作模拟
- gRPC API 参考文档: [API.md](./API.md)
- Protocol Buffers 语言指南: https://protobuf.dev/
- gRPC-Web 协议规范: https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-WEB.md
- Improbable gRPC-Web 实现: https://github.com/improbable-eng/grpc-web
