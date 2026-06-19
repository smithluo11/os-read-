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
