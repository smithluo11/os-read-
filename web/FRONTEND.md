# 前端实现配合文档

本文档帮助前端同学理解后端 API 的能力边界，指导 UI 界面的设计与功能实现。

---

## 1. gRPC-Web 连接指南

### 连接地址
```
http://localhost:18083
```

### 通信方式
后端提供 **双向流 RPC** `StreamSimulation`。前端建立连接后，可持续发送 `SimControlCommand` 指令，后端每次推送 `SystemSnapshot` 状态快照。

### 前端依赖
- `@improbable-eng/grpc-web` 或 `@protobuf-ts/grpcweb-transport`
- 生成的 Proto TypeScript/JavaScript 客户端代码

### 基本交互流程
```
1. 建立双向流连接
2. 发送 ACTION_INIT (+ ReadRequestConfig) → 接收初始 SystemSnapshot
3. 发送 ACTION_STEP_NEXT → 接收下一步 SystemSnapshot
4. 重复步骤 3，直到 is_finished = true
5. 可选：任意时刻发送 ACTION_INJECT_FAULT 注入故障，影响当前模拟
```

---

## 2. SimControlCommand 使用说明

### ACTION_INIT（初始化模拟）
需要填充 `config` 字段 (`ReadRequestConfig`)：
| 字段 | 类型 | 说明 | 示例值 |
|------|------|------|--------|
| `file_path` | string | 模拟读取的文件路径 | `"/home/user1/notes.txt"` |
| `bytes_to_read` | uint32 | 要读取的字节数 | `16384` |
| `user_buffer_addr` | uint64 | 用户缓冲区地址（用于地址越界检查） | `0x10000000` |
| `use_double_buffer` | bool | 是否启用双缓冲（乒乓）模式 | `true` |

可选 `user_context` 字段 (`UserContext`)：
| 字段 | 类型 | 说明 | 示例值 |
|------|------|------|--------|
| `uid` | uint32 | 用户 ID | `1000`（普通用户）/ `0`（root） |
| `gid` | uint32 | 组 ID | `1000` |
| `username` | string | 用户名 | `"user1"` / `"root"` |
| `home_dir` | string | 用户主目录 | `"/home/user1"` / `"/root"` |

若不传 `user_context`，后端默认为 `uid=1000, gid=1000, username=user1, home_dir=/home/user1`。

### ACTION_STEP_NEXT（单步推进）
无需填充额外字段。每次调用推动状态机走一层。

### ACTION_INJECT_FAULT（注入故障）
设置 `injected_fault` 字段 (`FaultType` 枚举)：

| 枚举值 | 含义 | 触发层 |
|--------|------|--------|
| `FAULT_NONE` (0) | 无故障 | — |
| `FAULT_PERMISSION_DENIED` (1) | 权限拒绝 | 设备无关层 |
| `FAULT_INVALID_ADDRESS` (2) | 非法地址 | 设备无关层 |
| `FAULT_HARDWARE_TIMEOUT` (3) | 硬件超时 | 硬件层 |
| `FAULT_PATH_TRAVERSAL` (4) | **新增** 路径穿越攻击 | 设备无关层 |
| `FAULT_FILE_NOT_FOUND` (5) | **新增** 文件不存在 | 设备无关层 |

---

## 3. Task 1: 用户切换 UI 设计

### 3.1 下拉菜单组件
建议在控制面板顶部放置「用户身份」下拉菜单：

```
┌─────────────────────────────────┐
│ 用户身份: [普通用户 ▼]          │
│   ├─ 👤 普通用户 (user1)        │
│   │   uid=1000, home=/home/user1│
│   └─ 🔒 Root (root)             │
│       uid=0, home=/root         │
└─────────────────────────────────┘
```

切换用户后，在发送 `ACTION_INIT` 时附带对应的 `user_context`。

### 3.2 推荐测试路径
| 测试场景 | 文件路径 | 用户 | 预期结果 |
|----------|----------|------|----------|
| 读自己的文件 ✅ | `/home/user1/notes.txt` | user1 (uid=1000) | 校验通过，继续执行 |
| 读敏感文件 ❌ | `/etc/shadow` | user1 (uid=1000) | `EACCES` 权限拒绝 |
| Root 读敏感文件 ✅ | `/etc/shadow` | root (uid=0) | 校验通过，继续执行 |
| 路径穿越攻击 ❌ | `../../etc/shadow` | user1 (uid=1000) | `EFAULT` 路径穿越检测 |
| 文件不存在 ❌ | `/nonexistent/file` | 任意 | `ENOENT` 文件不存在 |
| 读世界可写文件 ✅ | `/tmp/data.bin` | user1 (uid=1000) | 校验通过 |

### 3.3 可视化建议
- **绿色通道**：校验通过 → 大屏上看到「设备无关层」正常流转到「设备驱动层」
- **红色警报**：校验失败 → 大屏上「设备无关层」高亮爆红，显示错误码和详细原因
- 在 `step_description` 中已包含中文错误描述，可直接展示在 UI 中

---

## 4. Task 2: 双缓冲 Memory View 设计

### 4.1 开关控件
在 `ReadRequestConfig` 中设置 `use_double_buffer = true` 即可启用。
建议在控制面板添加 Toggle 开关：「单缓冲区 / 双缓冲（乒乓）」。

### 4.2 推荐测试参数
```
file_path: "/home/user1/notes.txt"
bytes_to_read: 16384        ← 16KB = 4 个 4KB chunk
use_double_buffer: true
```
此时 `total_chunks = 4`，状态机将循环 4 轮（USER→IND→DRV→HW→INT 首轮 + DRV→HW→INT 重复 3 次）。

### 4.3 Memory View 面板布局建议
```
┌──────────────────────────────────────────────────┐
│  Memory View                                      │
│                                                   │
│  内核缓冲区 1 (Kernel Buffer A)                   │
│  ┌──────────────────────────────────────────┐    │
│  │ [CHUNK_1_DATA] OS_DATA_BLOCK_1_0_1_2...  │    │
│  └──────────────────────────────────────────┘    │
│  状态: 🟢 硬件 DMA 写入中 (active_write_buffer)   │
│                                                   │
│  内核缓冲区 2 (Kernel Buffer B)                   │
│  ┌──────────────────────────────────────────┐    │
│  │ [CHUNK_0_DATA] OS_DATA_BLOCK_0_0_1_2...  │    │
│  └──────────────────────────────────────────┘    │
│  状态: 🔵 CPU copy_to_user 拷贝中 (active_read_buffer) │
│                                                   │
│  用户缓冲区 (User Buffer)                         │
│  ┌──────────────────────────────────────────┐    │
│  │ [CHUNK_0_START]OS_DATA_BLOCK_0...[CHUNK_ │    │
│  │ 1_DATA]OS_DATA_BLOCK_1...[CHUNK_2_DATA]  │    │
│  │ OS_DATA_BLOCK_2...[CHUNK_3_DATA]OS_DATA..│    │
│  └──────────────────────────────────────────┘    │
│  总大小: 16384 字节                               │
│                                                   │
│  进度: ████████░░░░░░░░ 2/4 (50%)                │
│  当前 IRP: IRP_READ -> Dev:Disk0, Len:16384      │
└──────────────────────────────────────────────────┘
```

### 4.4 缓冲区颜色方案
根据 `MemoryView` 中的追踪字段进行着色：

| 条件 | 颜色 | 含义 |
|------|------|------|
| `buffer_id == active_write_buffer` | 🟢 绿色 | 硬件正在 DMA 写入此缓冲区 |
| `buffer_id == active_read_buffer` | 🔵 蓝色 | CPU 正在从此缓冲区 copy_to_user |
| `buffer_id != active_write && buffer_id != active_read` | ⚪ 灰色 | 空闲中 |
| 首次填充时 (current_chunk==0) | 🟢 绿色 | 首块数据到达 |

判断逻辑伪代码：
```js
function getBufferColor(bufferId) {
    if (bufferId === snapshot.memoryState.activeWriteBuffer) return 'green';  // DMA 写入中
    if (bufferId === snapshot.memoryState.activeReadBuffer) return 'blue';    // CPU 拷贝中
    return 'gray';  // 空闲
}
```

### 4.5 进度条
```
进度: [current_chunk+1] / [total_chunks]
```
例如 `2/4` 表示已完成 2 个 chunk，剩余 2 个。当 `is_finished = true` 时进度条显示 100%。

### 4.6 用户缓冲区累加效果
随着每个中断步骤执行，`user_buffer_data` 会逐步增长：
- 第 1 次中断后：`[CHUNK_0_START]...`（~4KB）
- 第 2 次中断后：`[CHUNK_0_START]...[CHUNK_1_DATA]...`（~8KB）
- 第 3 次中断后：`[CHUNK_0_START]...[CHUNK_1_DATA]...[CHUNK_2_DATA]...`（~12KB）
- 第 4 次中断后：完整 16KB 数据拼接完成

前端建议添加滚动或自动展开的文本框来展示这个逐步累加的过程。

---

## 5. SystemSnapshot 字段参考

### 顶层字段
| 字段 | 类型 | 说明 |
|------|------|------|
| `current_active_layer` | Layer enum | 当前活跃的 I/O 层 |
| `step_description` | string | 当前步骤的中文描述 |
| `process_state` | ProcessBlock | 进程状态 |
| `memory_state` | MemoryView | 内存视图（缓冲区） |
| `hardware_state` | HardwareView | 硬件寄存器视图 |
| `is_finished` | bool | 模拟是否完成 |
| `final_error_code` | string | 错误码（成功时为 "SUCCESS"） |

### Layer 枚举
| 值 | 层 | 含义 |
|----|-----|------|
| `LAYER_USER` (0) | 用户层 | 用户进程调用 read() |
| `LAYER_INDEPENDENT` (1) | 设备无关层 | VFS 校验、权限检查 |
| `LAYER_DRIVER` (2) | 设备驱动层 | IRP 解析、寄存器编程 |
| `LAYER_INTERRUPT` (3) | 中断处理层 | 数据搬运、乒乓切换 |
| `LAYER_HARDWARE` (4) | 硬件层 | 磁盘读取、DMA 传输 |

### ProcessBlock
| 字段 | 类型 | 说明 |
|------|------|------|
| `pid` | uint32 | 进程 ID（默认 8888） |
| `state` | State enum | `STATE_RUNNING` / `STATE_BLOCKED` / `STATE_READY` |
| `wait_reason` | string | 阻塞原因（如 "等待物理硬件 I/O 中断信号"） |

### MemoryView
| 字段 | 类型 | 说明 |
|------|------|------|
| `user_buffer_data` | bytes | 用户空间缓冲区（逐步累加） |
| `kernel_buffer_1_data` | bytes | 内核缓冲区 1（Buffer A） |
| `kernel_buffer_2_data` | bytes | 内核缓冲区 2（Buffer B） |
| `current_irp_info` | string | I/O 请求包信息 |
| `active_write_buffer` | int32 | 硬件 DMA 目标缓冲区 (1 或 2) |
| `active_read_buffer` | int32 | CPU copy_to_user 源缓冲区 (1 或 2) |
| `current_chunk` | int32 | 当前数据块序号 (0-based) |
| `total_chunks` | int32 | 总数据块数 |

### HardwareView
| 字段 | 类型 | 说明 |
|------|------|------|
| `cmd_register` | string | 命令寄存器（如 "0x01: READ_SECTOR"） |
| `status_register` | string | 状态寄存器（如 "0x02: DEVICE_BUSY"） |
| `data_register` | bytes | 数据寄存器（从磁盘读取的原始数据） |

---

## 6. 推荐的前端框架栈

- **gRPC-Web 客户端**: `@protobuf-ts/grpcweb-transport` + `@protobuf-ts/runtime-rpc`
- **Proto 代码生成**: 将 `api/proto/io_simulation.proto` 编译为 TypeScript
- **UI 框架**: React / Vue / 任意
- **可视化**: Canvas 或 SVG 绘制 I/O 层状态图，配合 CSS 动画实现缓冲区闪烁效果

### TypeScript Proto 生成命令参考
```bash
protoc --proto_path=api/proto \
  --ts_out=web/src/generated \
  api/proto/io_simulation.proto
```

---

## 7. 答辩演示 — 层面联动四部曲

操作系统 I/O 核心就是「下发请求 (Top-Down)」与「中断回传 (Bottom-Up)」。
后端状态机 `USER → INDEPENDENT → DRIVER → HARDWARE → INTERRUPT → DRIVER↻` 的流转已为此量身定制。
前端大屏建议实现以下四阶段动态高亮。

### 7.1 联动起点：用户层 → 设备无关层

```
┌──────────────┐    系统调用     ┌─────────────────────────┐
│  USER LAYER  │ ──────────────→ │  INDEPENDENT LAYER (VFS) │
│  pid=8888    │   read(fd,buf)  │  6 级校验管线启动        │
│  uid=1000    │                 │  ACL 权限匹配            │
└──────────────┘                 └─────────────────────────┘
```

**UI 动画**：用户点击「发起读取」，用户层卡片高亮（展示进程 PID/UID 凭证），
一束光束传导到设备无关层卡片，该层逐一展示：
- 路径解析（栈弹出 `..` 的动画）
- ACL 表查找（FileSystemDB 逐条匹配高亮）
- 权限位比对（UID/GID vs Owner/Group 的绿色对勾或红色叉号）

**失败分支**：校验失败时设备无关层「爆红」，状态机熔断，错误码弹出。
**成功分支**：校验通过后生成 IRP（I/O 请求包），以数据包动画传递给驱动层。

### 7.2 联动中继：驱动层 → 硬件层

```
┌──────────────┐    写寄存器     ┌─────────────────────────┐
│ DRIVER LAYER │ ──────────────→ │   HARDWARE LAYER         │
│ 解析 IRP     │  cmd=0x01 READ  │  状态: READY → BUSY      │
│ 编程 DMA     │  DMA→Buf1/Buf2  │  DMAC 启动              │
└──────────────┘                 └─────────────────────────┘
```

**UI 动画**：驱动层卡片高亮，展示 cmd_register 从 `NO_OP` 变成 `READ_SECTOR`（绿色数字跳动）。
status_register 从 `READY` 变为 `DEVICE_BUSY`（黄色旋转图标）。
硬件层出现粗线条数据流动画：虚拟磁盘扇区 → 内核缓冲区 (Buffer A/B)，
数据如水流般灌入。

### 7.3 联动高潮：硬件层 → 中断层（异步的诞生）

```
┌──────────────┐    IRQ 中断     ┌─────────────────────────┐
│HW LAYER      │ ═══════════════→│  INTERRUPT LAYER (ISR)   │
│ DATA_READY   │  红色信号线闪烁  │  拷贝: HW→Kernel→User    │
│ 进程 BLOCKED │                 │  进程 等待唤醒            │
└──────────────┘                 └─────────────────────────┘
```

**UI 动画**：硬件层数据充满后，一条红色「IRQ 中断信号线」从硬件层闪烁拉通到中断层。
进程状态卡片显示 `BLOCKED`（橙色，带旋转等待图标）。
中断层执行两步拷贝动画：
1. `DataRegister` → `Kernel_Buffer_X`（黄色箭头）
2. `Kernel_Buffer_X` → `User_Buffer`（蓝色箭头，数据追加拼接到用户缓冲区尾部）

### 7.4 联动循环：中断层 → 驱动层（乒乓联动 ★核心）

```
┌──────────────┐   还有 chunk?   ┌─────────────────────────┐
│  INTERRUPT   │ ═══════════════→│  DRIVER LAYER (再入)     │
│ chunk N 完成 │  回溯虚线       │  切换 ActiveWriteBuffer  │
│ 累加数据     │                 │  编程下一块 DMA          │
└──────────────┘                 └─────────────────────────┘
         ↑                              │
         │      ┌──────────────┐        │
         └──────│ HW LAYER     │←───────┘
                │ 下一块 DMA   │
                └──────────────┘
```

**UI 动画**（最炫酷的联动）：
当 `current_chunk + 1 < total_chunks` 时，中断层处理完后，
一条 **虚线回溯箭头** 从「中断层」直接跳回「驱动层」。
驱动层再次高亮，`ActiveWriteBuffer` 从 1 切换到 2（或 2→1）的翻转动画。
驱动→硬件→中断三层开始自循环，直到所有 chunk 拼接完毕。
最后进程状态从 `BLOCKED` 变为 `RUNNING`（绿色），用户层弹出：**「数据读取成功，完整数据已累加！」**

### 7.5 联动面板实现参考

```js
// 每一帧 SystemSnapshot 到达时，根据 current_active_layer 切换高亮
function updateLayerHighlight(snapshot) {
    const layers = ['user', 'independent', 'driver', 'interrupt', 'hardware'];
    layers.forEach(l => {
        const el = document.getElementById(`layer-${l}`);
        el.classList.remove('active', 'error', 'complete');
    });

    const activeLayer = mapLayerToId(snapshot.currentActiveLayer);
    const el = document.getElementById(`layer-${activeLayer}`);

    if (snapshot.isFinished && snapshot.finalErrorCode !== 'SUCCESS') {
        el.classList.add('error');  // 红色闪烁
    } else if (snapshot.isFinished) {
        el.classList.add('complete');  // 绿色完成
    } else {
        el.classList.add('active');  // 蓝色高亮 + 脉冲动画
    }

    // 乒乓回溯特效：检测 layer 从 INTERRUPT 跳回 DRIVER
    if (prevLayer === 'interrupt' && activeLayer === 'driver') {
        showPingPongArrow();  // 显示回溯虚线箭头动画
    }
    prevLayer = activeLayer;
}
```

---

## 8. 答辩演示 — 设备无关层 (VFS) 三职责可视化

教科书上 VFS 的三个标准职责，后端代码完全有能力可视化展示：

```
+---------------------------------------------------------------+
|                   设备无关软件层 (VFS)                          |
+---------------------------------------------------------------+
|  1. 统一接口    →  统一接受 /etc/shadow 或 /tmp/data.bin       |
|     (Virtual FS)    屏蔽 ext4/NTFS/网络 FS 底层差异             |
|                                                               |
|  2. 缓冲管理    →  动态调度 Kernel Buffer 1 和 Buffer 2        |
|     (Buffer Mgmt)   单缓冲/双缓冲 模式切换                      |
|                                                               |
|  3. 差错控制    →  拦截 Path Traversal, Permission Denied     |
|     (Error Ctrl)    精确返回 EACCES/EFAULT/ENOENT/EIO          |
+---------------------------------------------------------------+
```

### 8.1 统一接口 (Uniform Interface) 展示

**教科书理论**：无论底层是 ext4、NTFS 还是网络文件系统，VFS 都向上提供统一的 `sys_read` 接口。

**前端实现**：
- 在 UI 中设计一个文件路径输入框 +「发起读取」按钮
- 让用户输入不同路径：`/home/user1/notes.txt`（普通文件）、`/etc/shadow`（系统敏感文件）、`/tmp/data.bin`（散设备文件）
- 所有请求都走同一个 gRPC `StreamSimulation` 入口，后端 `executeIndependentLayer()` 自动路由到 `ValidateFileAccess()`

**答辩台词**：
> 「无论用户输入什么路径，调用的都是同一个 gRPC 统一契约接口。
> 设备无关层自动将这些抽象路径映射到我们的模拟虚拟文件系统数据库中，
> 实现了对上层的完全透明。」

### 8.2 缓冲管理 (Buffer Management) 展示

**教科书理论**：内核统一分配和管理内核缓冲池，减少对慢速外设的直接访问。

**前端实现**：在 UI 右侧做一个「内核缓冲区状态面板」，并排两个方块：

```
┌──────────────────────────────────┐
│   内核缓冲区状态面板              │
│                                  │
│  ┌────────────┐  ┌────────────┐  │
│  │ Buffer A   │  │ Buffer B   │  │
│  │            │  │            │  │
│  │ [CHUNK_0] │  │  [等待中]   │  │
│  │            │  │            │  │
│  │  🔵 拷贝中 │  │  ⚪ 空闲   │  │
│  └────────────┘  └────────────┘  │
│                                  │
│  模式: 双缓冲 (乒乓)              │
│  active_write: 2  active_read: 1 │
└──────────────────────────────────┘
```

**配色方案**：
| 缓冲区状态 | 颜色 | CSS 动画 |
|-----------|------|---------|
| 硬件 DMA 写入中 (`active_write_buffer`) | 🟢 绿色 | `pulse-green` 脉冲 + 数据流入粒子 |
| CPU 拷贝读取中 (`active_read_buffer`) | 🔵 蓝色 | `pulse-blue` 脉冲 + 数据流出粒子 |
| 空闲 | ⚪ 灰色 | 静态 |

**答辩台词**：
> 「在设备无关层和中断层的协同下，我们动态管理着一组内核双缓冲区。
> 通过对 `active_write_buffer` 和 `active_read_buffer` 的精确控制，
> 完美模拟了 OS 缓冲池的预读和乒乓流转机制。」

### 8.3 差错控制 (Error Control) 展示

**教科书理论**：当发生越界、权限不足或硬件故障时，内核必须具备差错控制和保护机制。

**前端实现**：在 UI 底部做一个「差错控制演示面板」，包含预设场景按钮：

| 按钮 | 自动设置参数 | 预期捕获 |
|------|------------|---------|
| 🔴 模拟路径走私 | path=`../../etc/shadow`, user=user1 | `EFAULT` Path Traversal Detected |
| 🔴 模拟非法权限 | path=`/etc/shadow`, user=user1 | `EACCES` Permission Denied |
| 🔴 模拟文件不存在 | path=`/nonexistent/file`, user=user1 | `ENOENT` File Not Found |
| 🔴 模拟硬件超时 | 任意 path, inject=`FAULT_HARDWARE_TIMEOUT` | `EIO` Hardware Timeout |
| 🔴 模拟非法地址 | user_buffer_addr=`0xFFFFFFFF`, user=user1 | `EFAULT` Bad Address |

每个按钮点击后，自动发送 `ACTION_INIT` + 对应 config，前端直接 `ACTION_STEP_NEXT`，
状态机在设备无关层（或硬件层）**立刻熔断**，差错控制面板输出：
```
┌──────────────────────────────────────────────┐
│  差错控制台                                   │
│  ─────────────────────────────────────────── │
│  [ERROR] EFAULT: Path Traversal Detected!    │
│  Security Boundary Blocked.                  │
│  路径 "../../etc/shadow" 逃逸了用户主目录     │
│  "/home/user1"                               │
│  ─────────────────────────────────────────── │
│  [ERROR] EACCES: Permission Denied.          │
│  Active UID 1000 does not match Owner UID 0. │
│  文件 "/etc/shadow" 是敏感系统文件            │
│  ─────────────────────────────────────────── │
│  [ERROR] ENOENT: File Not Found.             │
│  路径 "/nonexistent/file" 在文件系统中不存在  │
└──────────────────────────────────────────────┘
```

**答辩台词**：
> 「我们的内核模拟器具备健全的内核级差错控制。在虚拟 VFS 层，
> 设计了多级防护管道。一旦触发路径穿越、权限越界或文件缺失，
> 差错控制模块会立刻熔断状态机，并向上层精确回传标准的 Unix 错误码。」

---

## 9. 答辩演示 — 乒乓缓冲 = 数据流管道化 (Pipelining)

### 9.1 向老师解释管道化概念

真实现代 OS 使用 Scatter-Gather DMA 或 DMA 传输链表实现多数据块跨地址 I/O。
我们的乒乓双缓冲状态机在逻辑上已完美模拟其核心效益：

```
时间轴 →

单缓冲 (串行瓶颈):
| DMA Chunk0 | CPU Copy Chunk0 | DMA Chunk1 | CPU Copy Chunk1 | ...
      ↑              ↑               ↑              ↑
   硬件忙          CPU忙          互相等待        互相等待

双缓冲 / 乒乓 (管道化并行):
| DMA Chunk0 → BufA | DMA Chunk1 → BufB | DMA Chunk2 → BufA | ...
|    (空闲)         | CPU Copy BufA     | CPU Copy BufB     | ...
      ↑                    ↑                   ↑
  硬件 DMA 管道        CPU 拷贝管道      两条管道时间重叠
```

**答辩台词**：
> 「当大文件被拆分为多个 Chunk 时，我们利用双内核缓冲区交替承载。
> 在硬件层通过 DMA 搬运当前数据块的同时，上层 CPU 异步并行拷贝上一个就绪的数据块。
> 这在逻辑上实现了一个 I/O 搬运与数据处理的并行流水线，
> 规避了传统单缓冲模式下 CPU 与外设互相等待的串行瓶颈。」

### 9.2 管道化可视化

前端可在 UI 底部添加一行「流水线时间轴」，当 `use_double_buffer=true` 时动态展示：

```
┌─────────────────────────────────────────────────────────────┐
│  I/O 流水线时间轴 (Pipeline Timeline)                        │
│                                                             │
│  DMA 管道:  ████████ Chunk0→Buf1 ████████ Chunk1→Buf2 ...  │
│  CPU 管道:  ········ (等待) ········ ░░░░░░░░ Copy Buf1 .. │
│                                                             │
│  ═══════════════════════════════════════════════════════    │
│  并行效率: DMA 与 CPU 时间窗口重叠，总耗时 ≈ max(T_dma, T_cpu)│
└─────────────────────────────────────────────────────────────┘
```

当单缓冲模式时，DMA 管道和 CPU 管道不重叠，直观对比出双缓冲的并发优势。

---

## 10. 动画策略 — 离散 Snapshot 到丝滑过渡

### 10.1 后端数据特征分析

后端是**纯事件驱动**模型：每次 `ACTION_STEP_NEXT` 产生恰好一个 `SystemSnapshot`，
没有帧率概念，没有时间戳，没有中间态。

```
后端数据流:
STEP_NEXT → Snapshot{t0}
STEP_NEXT → Snapshot{t1}    ← 两次 snapshot 之间时间间隔由用户点击速度决定
STEP_NEXT → Snapshot{t2}
```

**关键结论**：后端不需要改任何字段。`SystemSnapshot` 已包含前端做动画所需的**全部状态端点**。
前端只需在两个离散端点之间插入 CSS 缓动 / JS 动画帧。

### 10.2 可动画的状态变化量

每次 snapshot 到达时，对比上一帧，以下是前端可以驱动动画的 diff 维度：

| 变化字段 | 变化模式 | 动画方式 |
|----------|---------|---------|
| `current_active_layer` | 枚举跳变 (0→1→2→4→3→2↻) | 高亮光晕从旧卡片移动到新卡片 |
| `active_write_buffer` | 1↔2 翻转 | 缓冲区卡片边框颜色渐变 |
| `active_read_buffer` | 1↔2 翻转 | 缓冲区卡片边框颜色渐变 |
| `current_chunk` | 递增 0→1→2→3 | 进度条平滑增长 |
| `process_state.state` | RUNNING→BLOCKED→RUNNING | 状态标签颜色过渡 + 图标切换 |
| `hardware_state.status_register` | 字符串变化 | 寄存器值跳动 (数字翻转牌效果) |
| `hardware_state.cmd_register` | 字符串变化 | 同上 |
| `user_buffer_data.length` | 逐步增长 (append) | 字节计数滚动数字 |
| `kernel_buffer_*_data` | 交替非空 | 缓冲区内容淡入 |
| `is_finished` | false→true | 全部卡片归位动画 |
| `final_error_code` | SUCCESS→错误码 | 红色遮罩弹出 |

### 10.3 核心动画配方

#### 层级联动光晕 (Layer Highlight Transition)

```css
/* 每个层级卡片的基础样式 */
.layer-card {
    transition: box-shadow 0.4s ease-out,
                border-color 0.4s ease-out,
                transform 0.3s ease-out;
    border: 2px solid #334155;
}
.layer-card.active {
    border-color: #3b82f6;
    box-shadow: 0 0 20px rgba(59, 130, 246, 0.5);
    transform: scale(1.03);
}
.layer-card.error {
    border-color: #ef4444;
    box-shadow: 0 0 24px rgba(239, 68, 68, 0.7);
    animation: error-pulse 0.6s ease-in-out 3;
}
.layer-card.complete {
    border-color: #22c55e;
    box-shadow: 0 0 16px rgba(34, 197, 94, 0.4);
}

@keyframes error-pulse {
    0%, 100% { box-shadow: 0 0 24px rgba(239, 68, 68, 0.7); }
    50%      { box-shadow: 0 0 40px rgba(239, 68, 68, 1.0); }
}
```

CSS `transition` 在 class 变化时自动做 0.4s 缓动，无需 JS 动画库。
前端只需在收到新 snapshot 时更新 class：

```js
let prevSnapshot = null;

function applySnapshot(snap) {
    const prev = prevSnapshot;

    // 层级光晕移动
    document.querySelectorAll('.layer-card').forEach(el =>
        el.classList.remove('active', 'error', 'complete'));
    const activeEl = document.getElementById(layerId(snap.currentActiveLayer));
    if (snap.isFinished && snap.finalErrorCode !== 'SUCCESS') {
        activeEl.classList.add('error');
    } else if (snap.isFinished) {
        activeEl.classList.add('complete');
    } else {
        activeEl.classList.add('active');
    }

    // 缓冲区颜色：由 CSS transition 自动处理
    updateBufferColors(snap.memoryState);

    prevSnapshot = snap;
}
```

#### 缓冲区颜色渐变 (Buffer Color Transition)

```css
.kernel-buffer {
    transition: background-color 0.5s ease-out,
                border-color 0.5s ease-out,
                box-shadow 0.5s ease-out;
}
.kernel-buffer.write-target {
    background: rgba(34, 197, 94, 0.15);   /* 绿色底 — DMA 写入 */
    border-color: #22c55e;
    box-shadow: 0 0 12px rgba(34, 197, 94, 0.4);
}
.kernel-buffer.read-source {
    background: rgba(59, 130, 246, 0.15);  /* 蓝色底 — CPU 拷贝 */
    border-color: #3b82f6;
    box-shadow: 0 0 12px rgba(59, 130, 246, 0.4);
}
.kernel-buffer.idle {
    background: rgba(100, 116, 139, 0.08); /* 灰色 — 空闲 */
    border-color: #64748b;
    box-shadow: none;
}
```

```js
function updateBufferColors(mem) {
    const buf1 = document.getElementById('buffer-1');
    const buf2 = document.getElementById('buffer-2');
    [buf1, buf2].forEach(b => b.classList.remove('write-target', 'read-source', 'idle'));

    buf1.classList.add(getBufferRole(1, mem));
    buf2.classList.add(getBufferRole(2, mem));
}

function getBufferRole(id, mem) {
    if (id === mem.activeWriteBuffer) return 'write-target';
    if (id === mem.activeReadBuffer) return 'read-source';
    return 'idle';
}
```

#### 进度条平滑增长

```css
.progress-fill {
    transition: width 0.5s ease-out;
}
```

```js
const pct = ((snap.memoryState.currentChunk + 1) / snap.memoryState.totalChunks) * 100;
document.getElementById('progress-fill').style.width = `${pct}%`;
```

#### IRQ 中断信号线绘制动画

```css
.irq-line {
    stroke-dasharray: 1000;
    stroke-dashoffset: 1000;
    transition: stroke-dashoffset 0.6s ease-out;
}
.irq-line.active {
    stroke-dashoffset: 0;
    stroke: #ef4444;
    filter: drop-shadow(0 0 4px rgba(239, 68, 68, 0.8));
}
```

#### 乒乓回溯虚线动画

```css
.pingpong-arrow {
    stroke-dasharray: 8 4;
    stroke-dashoffset: 0;
    opacity: 0;
    transition: opacity 0.3s ease-out;
}
.pingpong-arrow.visible {
    opacity: 1;
    animation: dash-flow 0.8s linear infinite;
}

@keyframes dash-flow {
    to { stroke-dashoffset: -24; }
}
```

```js
// 在 snapshot 处理中检测乒乓回溯
if (prev && prev.currentActiveLayer === 'INTERRUPT'
    && snap.currentActiveLayer === 'DRIVER'
    && snap.memoryState.currentChunk > 0) {
    document.getElementById('pingpong-arrow').classList.add('visible');
    setTimeout(() => {
        document.getElementById('pingpong-arrow').classList.remove('visible');
    }, 1200);
}
```

### 10.4 数据流入粒子动画

从硬件层到内核缓冲区之间，可以画一条粒子管道：

```js
// 使用 Canvas 或 SVG，在 requestAnimationFrame 中逐帧更新粒子位置
const particles = [];
const PATH_POINTS = [
    {x: 400, y: 200},  // 硬件层出口
    {x: 400, y: 350},  // 内核缓冲区入口
];

function spawnParticles(count) {
    for (let i = 0; i < count; i++) {
        particles.push({ t: i / count, speed: 0.003 + Math.random() * 0.005 });
    }
}

function animateParticles() {
    particles.forEach(p => {
        p.t += p.speed;
        if (p.t > 1) p.t -= 1; // 循环
        const pos = lerpPath(PATH_POINTS, p.t);
        drawParticle(pos.x, pos.y);
    });
    requestAnimationFrame(animateParticles);
}

// 当 active_write_buffer 变化时触发粒子生成
if (mem.activeWriteBuffer !== prevMem?.activeWriteBuffer) {
    spawnParticles(30);
}
```

### 10.5 动画时序总览

以一次完整的双缓冲 16KB 读取为例（4 chunks，14 个 snapshot）：

```
Snapshot  LAYER        触发的前端动画                   CSS 时长
────────  ────────────  ──────────────────────────────  ────────
S0        USER          用户卡片高亮 + 按钮禁用         0.4s
S1        INDEPENDENT   光晕移到 VFS → 6级校验逐条亮起  0.4s x 6
                        IRP 数据包飞向驱动层            0.5s
S2        DRIVER        光晕移到驱动 → cmd 寄存器跳动   0.4s
                        process 变 BLOCKED (橙色)      0.3s
S3        HARDWARE      data_register 填充动画          0.3s
                        粒子从磁盘流入 Buffer 1         持续
S4        INTERRUPT     IRQ 红线闪烁拉通                0.6s
                        Buffer1 绿→蓝 (角色切换)        0.5s
                        UserBuffer 字节计数跳动         0.4s
S5        DRIVER ←──    乒乓回溯虚线 (关键!)            0.8s
                        Buffer2 变绿色 DMA 目标         0.5s
S6        HARDWARE      粒子流入 Buffer 2               持续
S7        INTERRUPT     IRQ 再次触发                    0.6s
                        Buffer2 绿→蓝                   0.5s
S8        DRIVER ←──    乒乓回溯                        0.8s
...循环...
S13       INTERRUPT     is_finished=true                —
                        所有卡片绿色完成动画            0.5s
                        process RUNNING (绿色)          0.3s
                        大屏弹出 "读取 16384 字节成功"  0.6s
```

### 10.6 关键原则

1. **所有动画都发生在两次 Snapshot 之间的"间隙"**：前端收到新 Snapshot → 触发 CSS transition / JS 动画 → 动画结束，等待用户下一次点击
2. **后端不需任何改动**：`SystemSnapshot` 的离散状态端点已经足够驱动全部过渡动画
3. **动画时长控制在 300-800ms**：太短看不清联动，太长拖慢答辩节奏
4. **乒乓回溯虚线是最关键的视觉锚点**：它让学生/老师直观理解「中断层如何重新触发驱动层」
5. **差错熔断用 3 次红色脉冲 (`error-pulse`)**：视觉冲击力强，一眼看出「出错了」

---

## 11. 防御性编码 — 动画稳健落地细节

### 11.1 应对「连点侠」的动画打断机制

**风险场景**：答辩演示时连续快速点击 `ACTION_STEP_NEXT`，前一个 Snapshot 的 500ms
动画还没播完，下一个 Snapshot 已到达，导致动画叠加、粒子轨迹错乱或卡顿。

**核心武器：CSS transition 的原生中断接管能力**

当 CSS 属性在 transition 中途被修改时，浏览器会**自动从当前中间值（computed style）
平滑过渡到新目标值**，无需手动计算剩余时间。这意味着：

```js
// 前端只需立即更新属性，浏览器自动处理动画中断
function onSnapshotArrived(snap) {
    // 1. 直接覆盖属性 — CSS transition 自动从中断点平滑过渡
    updateLayerHighlight(snap);      // class 切换，0.4s transition
    updateBufferColors(snap.memoryState); // class 切换，0.5s transition
    updateProgressBar(snap);         // width 更新，0.5s transition

    // 2. rAF 动画需要手动取消 + 重置
    cancelRAF();       // 取消上一轮粒子动画帧
    resetParticles();  // 清空粒子数组
    spawnParticlesForCurrentState(snap); // 从新状态端点重新生成粒子
}
```

**关键规则**：

| 动画类型 | 中断策略 | 原因 |
|----------|---------|------|
| CSS transition（光晕、颜色、进度条） | **直接覆盖属性**，不做任何保护 | 浏览器自动从 computed style 平滑过渡 |
| CSS animation（脉冲、流动虚线） | 移除 class 触发的 animation → 重新添加 | 每次新 snapshot 重新设定 animation 起点 |
| JS rAF（粒子系统） | **必须**调用 `cancelAnimationFrame` + 清空粒子数组 | rAF 没有内置中断，需手动销毁重建 |

```js
// 完整的 Snapshot 消费函数（含动画中断处理）
let rafId = null;
let activeParticles = [];

function consumeSnapshot(snap) {
    // CSS 动画：直接覆盖，无保护（浏览器原生处理中断）
    applyCSSTransitions(snap);

    // JS 动画：必须手动取消
    if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
    }
    activeParticles = [];

    // 从新状态重新生成粒子
    if (shouldSpawnParticles(snap)) {
        rafId = requestAnimationFrame(particleLoop);
    }
}
```

**「STEP_NEXT 按钮防抖」与「动画打断」的本质区别**：
- **防抖 (Debounce)**：阻止用户快速点击 → ❌ 会让答辩操作感迟钝，不推荐
- **动画打断 (Override)**：允许用户快速点击，但每次新 Snapshot 到达时强制覆盖动画状态 → ✅ 操作跟手，动画不堆叠


### 11.2 SVG 中断回传线的流向控制

§7 中的 IRQ 信号线和乒乓回溯虚线，运动方向必须与数据流/控制流**绝对一致**。

**核心原理**：
- `stroke-dashoffset` **递减** → 虚线向前流动（从路径起点到终点）
- `stroke-dashoffset` **递增** → 虚线向后流动（从路径终点到起点）

**实现方案**：用同一条 SVG 路径，通过 CSS 变量控制流向，避免画两遍线：

```html
<svg>
    <!-- 下发请求线：硬件层 ← 驱动层（Top-Down） -->
    <path id="topdown-line" class="flow-line flow-down"
          d="M 350 150 L 350 250" />

    <!-- 中断回传线：中断层 ← 硬件层（Bottom-Up） -->
    <path id="irq-line" class="flow-line flow-up"
          d="M 450 250 L 450 150" />
</svg>
```

```css
.flow-line {
    fill: none;
    stroke-width: 3;
    stroke-dasharray: 10 6;
}
/* 下发请求：dashoffset 递减 → 正向流动 (绿线) */
.flow-down {
    stroke: #22c55e;
    animation: flow-forward 0.8s linear infinite;
}
/* 中断回传：dashoffset 递增 → 反向流动 (红线) */
.flow-up {
    stroke: #ef4444;
    animation: flow-reverse 0.6s linear infinite;
}

@keyframes flow-forward {
    to { stroke-dashoffset: -32; }  /* 递减 = 正向 */
}
@keyframes flow-reverse {
    to { stroke-dashoffset: 32; }   /* 递增 = 反向 */
}
```

**流向校验表**：

| 连线 | 方向 | CSS animation | 含义 |
|------|------|---------------|------|
| INDEPENDENT → DRIVER | ↓ 下发 | `flow-forward` (dashoffset 递减) | IRP 请求包向下传递 |
| DRIVER → HARDWARE | ↓ 下发 | `flow-forward` | 寄存器命令下发 |
| HARDWARE → INTERRUPT | ↑ 回传 | `flow-reverse` (dashoffset 递增) | IRQ 中断信号向上回传 |
| INTERRUPT → DRIVER | ↻ 回溯 | `flow-forward` + 虚线 | 乒乓循环，新 DMA 命令下发 |

**触发时机**：

```js
function updateFlowLines(snap, prev) {
    const irqLine = document.getElementById('irq-line');
    const pingpongLine = document.getElementById('pingpong-line');

    // HARDWARE → INTERRUPT: 激活 IRQ 回传线
    if (snap.currentActiveLayer === 'INTERRUPT'
        && prev?.currentActiveLayer === 'HARDWARE') {
        irqLine.classList.add('active');
    } else {
        irqLine.classList.remove('active');
    }

    // INTERRUPT → DRIVER (乒乓回溯): 激活回溯虚线
    if (snap.currentActiveLayer === 'DRIVER'
        && prev?.currentActiveLayer === 'INTERRUPT'
        && snap.memoryState.currentChunk > 0) {
        pingpongLine.classList.add('active');
    } else {
        pingpongLine.classList.remove('active');
    }
}
```


### 11.3 数据流入粒子的轻量化实现

**目标**：`kernel_buffer_*_data` 填充时，从硬件层到缓冲区的数据流入动画，
在低端笔记本 / 答辩投影仪上稳定跑满 60fps。

**核心原则：确定性伪粒子，零物理引擎**

不需要复杂的物理模拟。路径已知（硬件层出口 → 缓冲区入口），只需维护一个
`progress` 数组，每帧沿贝塞尔曲线插值绘制 3~5 个小圆点。

```js
// 确定性粒子系统 — 无物理引擎，纯数学插值
const PARTICLE_COUNT = 4;  // 仅 4 个粒子，极轻量
const BEZIER_POINTS = [
    {x: 400, y: 180},  // P0: 硬件层出口
    {x: 420, y: 220},  // P1: 控制点 (弯向右侧)
    {x: 400, y: 260},  // P2: 控制点
    {x: 400, y: 310},  // P3: 内核缓冲区入口
];

let particles = [];
let rafId = null;

// 启动粒子流 — 在 active_write_buffer 变化时调用
function startParticleFlow(targetBufferId) {
    cancelParticleFlow();  // 防御：先销毁上一轮
    particles = [];
    for (let i = 0; i < PARTICLE_COUNT; i++) {
        particles.push({
            t: i / PARTICLE_COUNT,          // 0.0 ~ 1.0 均匀分布
            speed: 0.008,                    // 固定速度，确定性行为
            target: targetBufferId,
        });
    }
    rafId = requestAnimationFrame(particleLoop);
}

// 销毁粒子流
function cancelParticleFlow() {
    if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
    }
    particles = [];
}

function particleLoop() {
    const ctx = document.getElementById('particle-canvas').getContext('2d');
    ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);

    particles.forEach(p => {
        p.t += p.speed;
        if (p.t > 1.0) p.t -= 1.0;  // 循环流动

        // 三次贝塞尔插值: B(t) = (1-t)³P0 + 3(1-t)²tP1 + 3(1-t)t²P2 + t³P3
        const pos = cubicBezier(BEZIER_POINTS, p.t);
        ctx.beginPath();
        ctx.arc(pos.x, pos.y, 4, 0, Math.PI * 2);
        ctx.fillStyle = p.target === 1 ? '#22c55e' : '#3b82f6';
        ctx.fill();
    });

    rafId = requestAnimationFrame(particleLoop);
}

function cubicBezier(pts, t) {
    const mt = 1 - t;
    const mt2 = mt * mt, mt3 = mt2 * mt;
    const t2 = t * t, t3 = t2 * t;
    return {
        x: mt3 * pts[0].x + 3 * mt2 * t * pts[1].x + 3 * mt * t2 * pts[2].x + t3 * pts[3].x,
        y: mt3 * pts[0].y + 3 * mt2 * t * pts[1].y + 3 * mt * t2 * pts[2].y + t3 * pts[3].y,
    };
}
```

**为什么这是「降维打击」级别的实现**：

| 方案 | 粒子数/帧 | 计算量 | 60fps 保障 |
|------|----------|--------|-----------|
| 物理引擎 (matter.js 等) | 50+ | 碰撞检测 + 重力积分 | ❌ 低端设备掉帧 |
| 本方案 (确定性伪粒子) | 4 | 4 次三次贝塞尔求值 | ✅ 稳定 60fps |

每个粒子仅需 17 次浮点运算（三次贝塞尔插值），4 个粒子总计 68 次浮点运算/帧，
在任何设备上都能稳定跑满 60fps，同时视觉效果与真粒子流无异。


### 11.4 答辩降维打击话术 — 「离散状态 · 时序内插」架构

当评委问「后端是离散状态机，为什么前端能看到丝滑的硬件中断和流水线过程？
后端是不是开了高频定时器推送？」时，直接抛出以下架构阐述：

> 「报告老师，这正是本系统在架构上做的一点创新。
>
> 为了保证内核状态机的**绝对纯净与确定性**，后端采用了纯事件驱动模型，
> 不引入任何与业务无关的帧率概念，每次只抛出高内聚的 `SystemSnapshot`。
>
> 而在展示层，我们设计了一套基于**差异比对（Diff-driven）的前端微状态机**。
> 前端消费离散的 Snapshot，通过解析状态跳变，动态激活 CSS Transition 状态机
> 与 Web 矢量动画（SVG 路径插值 + 确定性粒子系统）。
>
> 用前端的『时序内插』去还原底层的『连续物理过程』。
> 这样既保证了后端内核的开发效率与确定性，又实现了极具表现力的教学可视化效果。」

这段答辩话术直接封死了评委的两个潜在质疑：
1. 「后端是不是开了定时器推帧」→ 不，纯事件驱动，无帧率概念
2. 「前端动画是不是虚假的，跟后端逻辑脱节」→ 不，前端每一帧动画都由 Snapshot diff 驱动，状态严格同步
