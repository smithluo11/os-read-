# I/O Simulator — gRPC API 参考文档

> 服务名: `io_simulator.IOSimulationEngine`  
> 传输: gRPC-Web over WebSocket (Improbable 协议)  
> 端口: `18083`

---

## 1. 服务定义

```protobuf
service IOSimulationEngine {
  rpc StreamSimulation(stream SimControlCommand) returns (stream SystemSnapshot);
}
```

双向流模式：前端发送控制指令流，后端持续推送状态快照。

### 通信时序

```
前端                              后端
 │                                │
 │──── ACTION_INIT ──────────────▶│  初始化状态机
 │◀─── SystemSnapshot (initial) ──│  推送初始快照
 │                                │
 │──── ACTION_STEP_NEXT ─────────▶│  推进一个子步骤
 │◀─── SystemSnapshot ────────────│  推送最新快照
 │       ... (重复 N 次) ...       │
 │◀─── SystemSnapshot (finished) ─│  is_finished = true
 │                                │
 │──── ACTION_INJECT_FAULT ──────▶│  注入故障（可选，INIT 后）
```

---

## 2. 控制指令 — `SimControlCommand`

| 字段 | 类型 | 说明 |
|------|------|------|
| `action` | `Action` enum | 指令类型 |
| `config` | `ReadRequestConfig` | 读配置（INIT 时必填） |
| `injected_fault` | `FaultType` enum | 故障注入类型（INJECT_FAULT 时用） |
| `user_context` | `UserContext` | 用户身份上下文 |

### Action 枚举

| 值 | 名称 | 说明 |
|----|------|------|
| 0 | `ACTION_INIT` | 初始化模拟，需附带 `config` 和 `user_context` |
| 1 | `ACTION_STEP_NEXT` | 推进一个子步骤 |
| 2 | `ACTION_INJECT_FAULT` | 注入故障，需附带 `injected_fault` |

---

## 3. 读配置 — `ReadRequestConfig`

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `file_path` | `string` | `/home/user1/notes.txt` | 目标文件路径 |
| `bytes_to_read` | `uint32` | `16384` | 读取字节数（128~65536） |
| `user_buffer_addr` | `uint64` | `0x10000000` | 用户空间缓冲区地址 |
| `use_double_buffer` | `bool` | `true` | 是否启用双缓冲（乒乓） |
| `use_page_cache` | `bool` | `true` | 是否启用页缓存 |

### 地址校验规则
- `user_buffer_addr < 0xC0000000` → 合法用户空间地址
- `user_buffer_addr >= 0xC0000000` → 触发 `EFAULT (Bad address)`

---

## 4. 故障类型 — `FaultType`

| 值 | 名称 | 终止? | 触发层 | 错误码 |
|----|------|-------|--------|--------|
| 0 | `FAULT_NONE` | — | — | SUCCESS |
| 1 | `FAULT_PERMISSION_DENIED` | ✅ 终止 | L1 VFS sub-step 2 | EACCES |
| 2 | `FAULT_INVALID_ADDRESS` | ✅ 终止 | L0 USER sub-step 3 | EFAULT |
| 3 | `FAULT_HARDWARE_TIMEOUT` | ✅ 终止 | L4 HW sub-step 2 | EIO |
| 4 | `FAULT_PATH_TRAVERSAL` | ✅ 终止 | L1 VFS sub-step 2 | EPERM |
| 5 | `FAULT_FILE_NOT_FOUND` | ✅ 终止 | L1 VFS sub-step 2 | ENOENT |
| 6 | `FAULT_EAGAIN` | 🔄 可恢复 | L2 DRV sub-step 2 | — (重试 3 次后继续) |

### EAGAIN 行为
- 模拟**不终止**，驱动层停留在 sub-step 2
- 每次用户点击「下一步」= 一次重试
- 3 次重试后设备就绪，正常继续 I/O

---

## 5. 用户上下文 — `UserContext`

| 字段 | 类型 | 说明 |
|------|------|------|
| `uid` | `uint32` | 用户 ID（0=root 绕过权限检查） |
| `gid` | `uint32` | 组 ID |
| `username` | `string` | 用户名 |
| `home_dir` | `string` | 用户主目录（路径穿越防御基准） |

### 预设用户

| 用户名 | uid | gid | home_dir |
|--------|-----|-----|----------|
| root | 0 | 0 | `/root` |
| user1 | 1000 | 1000 | `/home/user1` |

---

## 6. 系统快照 — `SystemSnapshot`

每次 `ACTION_STEP_NEXT` 的响应。

| 字段 | 类型 | 说明 |
|------|------|------|
| `current_active_layer` | `Layer` enum | 当前活动层 |
| `step_description` | `string` | 当前步骤的中文描述（用于前端展示） |
| `sub_step` | `int32` | 当前层内子步骤序号（1-based） |
| `total_sub_steps` | `int32` | 当前层子步骤总数 |
| `process_state` | `ProcessBlock` | 进程状态 |
| `memory_state` | `MemoryView` | 内存/缓冲/缓存状态 |
| `hardware_state` | `HardwareView` | 硬件/DMA 寄存器状态 |
| `is_finished` | `bool` | 模拟是否结束 |
| `final_error_code` | `string` | 终态错误码（SUCCESS 表示正常） |

### Layer 枚举

| 值 | 名称 | 层次 |
|----|------|------|
| 0 | `LAYER_USER` | L0 用户层 I/O 软件 |
| 1 | `LAYER_INDEPENDENT` | L1 设备无关软件 (VFS) |
| 2 | `LAYER_DRIVER` | L2 设备驱动程序 |
| 3 | `LAYER_INTERRUPT` | L3 中断处理程序 (ISR) |
| 4 | `LAYER_HARDWARE` | L4 硬件层 (磁盘控制器) |

### 层级流转

```
L0 USER (4步) → L1 VFS (5步) → L2 DRV (3步) → L4 HW (3步) → L3 INT (3步)
                                                      ↑              │
                                                      └── 乒乓循环 ──┘
```

| 层 | 子步骤数 | 关键内容 |
|----|---------|---------|
| L0 用户层 | 4 | fread→fd查找→access_ok→陷入内核 |
| L1 VFS | 5 | 路径解析→ACL→**页缓存查找**→缓冲分配→IRP构造 |
| L2 驱动 | 3 | IRP解析→寄存器编程→进程阻塞 |
| L4 硬件 | 3 | DMA启动→磁盘读取→硬件中断 |
| L3 中断 | 3 | ISR接管→数据拷贝→乒乓切换/I/O完成 |

---

## 7. 进程状态 — `ProcessBlock`

| 字段 | 类型 | 说明 |
|------|------|------|
| `pid` | `uint32` | 进程 ID（固定 8888） |
| `state` | `State` enum | 进程状态 |
| `wait_reason` | `string` | 阻塞原因 |

### State 枚举

| 值 | 名称 | 说明 |
|----|------|------|
| 0 | `STATE_RUNNING` | 运行中 |
| 1 | `STATE_BLOCKED` | 阻塞（等待 I/O） |
| 2 | `STATE_READY` | 就绪 |

---

## 8. 内存视图 — `MemoryView`

| 字段 | 类型 | 说明 |
|------|------|------|
| `user_buffer_data` | `bytes` | 用户缓冲区数据（累积） |
| `kernel_buffer_1_data` | `bytes` | 内核缓冲 A 数据 |
| `kernel_buffer_2_data` | `bytes` | 内核缓冲 B 数据 |
| `current_irp_info` | `string` | 当前 IRP 描述 |
| `active_write_buffer` | `int32` | DMA 写入目标（1 或 2） |
| `active_read_buffer` | `int32` | 用户读取源（1 或 2） |
| `current_chunk` | `int32` | 当前数据块序号（0-based） |
| `total_chunks` | `int32` | 总数据块数 |
| `cache_hit` | `bool` | 当前读是否页缓存命中 |
| `cached_pages` | `uint32` | 页缓存中的总页数 |
| `retry_count` | `uint32` | EAGAIN 重试计数 |
| `retry_max` | `uint32` | EAGAIN 最大重试次数 |

---

## 9. 硬件视图 — `HardwareView`

| 字段 | 类型 | 说明 |
|------|------|------|
| `cmd_register` | `string` | 命令寄存器 |
| `status_register` | `string` | 状态寄存器 |
| `data_register` | `bytes` | 数据寄存器 |
| `dma_source` | `string` | DMA 源地址 |
| `dma_destination` | `string` | DMA 目标地址 |
| `dma_count` | `uint32` | DMA 传输字节数 |
| `dma_status` | `string` | DMA 状态 |

### 寄存器典型值

| 寄存器 | 阶段 | 值 |
|--------|------|----|
| CMD_REG | 空闲 | `0x00: NO_OP` |
| CMD_REG | 驱动编程后 | `0x01: READ_SECTOR (...)` |
| STS_REG | 空闲 | `0x01: READY` |
| STS_REG | 设备忙 | `0x02: DEVICE_BUSY` |
| STS_REG | 数据就绪 | `0x04: DATA_READY` |
| STS_REG | 硬件错误 | `0x03: DEVICE_ERROR` |
| DMA_STATUS | 空闲 | `IDLE` |
| DMA_STATUS | 驱动编程后 | `SETUP` |
| DMA_STATUS | 数据传输中 | `TRANSFERRING` |
| DMA_STATUS | 传输完成 | `DONE` |
| DMA_STATUS | 硬件超时 | `ERROR` |

---

## 10. 错误码速查

| 错误码 | 含义 | 触发条件 |
|--------|------|---------|
| `SUCCESS` | 正常完成 | 全部子步骤顺利执行 |
| `EBADF` | 无效文件描述符 | fd 不在进程打开文件表 / 只写打开 |
| `EFAULT` | 非法地址 | 用户缓冲区地址越界 / 路径穿越 |
| `EACCES` | 权限不足 | 权限位拒绝 / 敏感文件非 root |
| `ENOENT` | 文件不存在 | 路径不在文件系统数据库中 |
| `EPERM` | 路径遍历攻击 | 检测到 `../` 模式逃逸主目录 |
| `EIO` | I/O 错误 | 硬件响应超时 / 磁盘错误 |

---

## 11. 模拟文件系统

| 路径 | Owner | Group | 权限 | 敏感 | 说明 |
|------|-------|-------|------|------|------|
| `/home/user1/notes.txt` | 1000 | 1000 | 0644 | ❌ | 普通文件 |
| `/home/user1/public.txt` | 1000 | 1000 | 0644 | ❌ | 普通文件 |
| `/tmp/data.bin` | 1000 | 1000 | 0666 | ❌ | 全局可读写 |
| `/etc/shadow` | 0 | 0 | 0600 | ✅ | 仅 root |
| `/sys/kernel/secure` | 0 | 0 | 0400 | ✅ | 仅 root |
| `/root/secret.key` | 0 | 0 | 0400 | ✅ | 仅 root |

### 权限判定规则

```
if uid == 0                → 允许 (root 绕过)
if uid == file.OwnerUID    → owner read bit  (0644 → rw-r--r--)
if gid == file.GroupGID    → group read bit  (0644 → rw-r--r--)
else                       → other read bit  (0644 → rw-r--r--)
```

---

## 12. 完整流程示例

### 正常单缓冲 (18 步)

```
[S1]  L0 1/4  库函数调用: fread() → libc
[S2]  L0 2/4  fd 表查找: fd=3 → OpenFileEntry
[S3]  L0 3/4  access_ok: 地址校验通过
[S4]  L0 4/4  陷入内核: int 0x80 → Ring0
[S5]  L1 1/5  路径解析: /home/user1/notes.txt → inode
[S6]  L1 2/5  ACL 校验: 4 级管线通过
[S7]  L1 3/5  页缓存查找: 未命中 → 需要磁盘 I/O
[S8]  L1 4/5  缓冲分配: 单缓冲区
[S9]  L1 5/5  IRP 构造: IRP_READ → Disk0
[S10] L2 1/3  IRP 解析: 设备=Disk0, 长度=16384
[S11] L2 2/3  寄存器编程: CMD=READ_SECTOR, STS=BUSY
[S12] L2 3/3  进程阻塞: BLOCKED
[S13] L4 1/3  DMA 启动: 源=Disk, 目标=Kernel Buf1, 16384B
[S14] L4 2/3  磁盘读取: 数据→DATA_REG, DMA DONE
[S15] L4 3/3  硬件中断: IRQ#14 → CPU
[S16] L3 1/3  ISR 接管: 保存上下文
[S17] L3 2/3  数据拷贝: HW→Kernel→User (16384B)
[S18] L3 3/3  I/O 完成: sys_read()=16384, 进程→RUNNING
```

### 缓存命中 (7 步)

```
[S1-S6]  同上 L0+L1 (path→ACL)
[S7]     L1 3/5  页缓存 命中 ✓: 数据从 RAM 直接返回
         is_finished = true  ← 跳过 L2/L4/L3
```
