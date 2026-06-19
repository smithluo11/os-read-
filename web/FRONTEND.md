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
