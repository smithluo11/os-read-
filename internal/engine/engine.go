package engine

import (
	"errors"
	"fmt"
	"io-simulator/api/pb"
)

const chunkSize = 4096 // 4KB per chunk (模拟磁盘扇区/页大小)

// OpenFileEntry 进程打开文件表 (fd table) 中的条目
type OpenFileEntry struct {
	Fd       int32  // 文件描述符 (如 3)
	FilePath string // 关联的文件路径
	Flags    int32  // 打开标志: 0=O_RDONLY
	FilePos  int64  // 当前文件读写偏移量
	InodePtr string // 指向 inode 的指针标识 (模拟)
}

// SimulationEngine 掌控全局状态机
type SimulationEngine struct {
	CurrentLayer pb.SystemSnapshot_Layer
	SubStep      int32 // 当前层内子步骤序号 (1-based), 0 表示刚进入该层
	StepIndex    int
	Config       *pb.ReadRequestConfig
	Snapshot     *pb.SystemSnapshot
	InjectedFault pb.FaultType
	UserContext  *pb.UserContext

	// L0 用户层数据结构
	FdTable   []*OpenFileEntry // 进程打开文件表
	CurrentFd int32            // 当前 read() 操作的文件描述符

	// 双缓冲 / 乒乓缓冲状态
	ActiveWriteBuffer int32
	ActiveReadBuffer  int32
	CurrentChunk      int32
	TotalChunks       int32

	// 页缓存 (Page Cache) — 模拟内核文件页缓存
	PageCache   map[string][]byte // key: filePath, value: cached data
	CacheMissed bool              // 当前读是否缓存未命中 (用于 ISR 完成后回写)

	// EAGAIN 重试状态
	RetryCount   uint32
	RetryMax     uint32
	retrySubStep bool // NextStep 中跳过 SubStep 推进（EAGAIN 重试用）
}

// NewEngine 初始化一次全新的 read 模拟
func NewEngine(config *pb.ReadRequestConfig, userCtx *pb.UserContext) *SimulationEngine {
	totalChunks := int32(1)
	if config.UseDoubleBuffer && config.BytesToRead > 0 {
		totalChunks = int32((config.BytesToRead + chunkSize - 1) / chunkSize)
	}

	// 初始化页缓存
	var pageCache map[string][]byte
	if config.UsePageCache {
		pageCache = make(map[string][]byte)
	}

	return &SimulationEngine{
		CurrentLayer:  pb.SystemSnapshot_LAYER_USER,
		CacheMissed:   false,
		PageCache:     pageCache,
		RetryMax:      3,
		SubStep:       0, // 首次 NextStep 会推进到 1
		StepIndex:     0,
		Config:        config,
		InjectedFault: pb.FaultType_FAULT_NONE,
		UserContext:   userCtx,

		// 初始化进程打开文件表 (模拟 fd=3 对应目标文件)
		FdTable: []*OpenFileEntry{
			{Fd: 3, FilePath: config.FilePath, Flags: 0, FilePos: 0, InodePtr: "inode:0xBEEF"},
		},
		CurrentFd: 3,

		ActiveWriteBuffer: 1,
		ActiveReadBuffer:  1,
		CurrentChunk:      0,
		TotalChunks:       totalChunks,

		Snapshot: &pb.SystemSnapshot{
			CurrentActiveLayer: pb.SystemSnapshot_LAYER_USER,
			StepDescription:    "等待用户发起 read() 系统调用...",
			SubStep:            0,
			TotalSubSteps:      4,
			ProcessState: &pb.ProcessBlock{
				Pid:   8888,
				State: pb.ProcessBlock_STATE_RUNNING,
			},
			MemoryState: &pb.MemoryView{
				UserBufferData:     []byte{},
				KernelBuffer_1Data: []byte{},
				KernelBuffer_2Data: []byte{},
				CurrentIrpInfo:     "IDLE",
				ActiveWriteBuffer:  1,
				ActiveReadBuffer:   1,
				CurrentChunk:       0,
				TotalChunks:        totalChunks,
				RetryCount:         0,
				RetryMax:           3,
			},
			HardwareState: &pb.HardwareView{
				CmdRegister:      "0x00: NO_OP",
				StatusRegister:   "0x01: READY",
				DataRegister:     []byte{},
				DmaSource:        "—",
				DmaDestination:   "—",
				DmaCount:         0,
				DmaStatus:        "IDLE",
			},
			IsFinished:     false,
			FinalErrorCode: "SUCCESS",
		},
	}
}

// NextStep 核心驱动：前端每点一次"下一步"，推进一个子步骤
func (e *SimulationEngine) NextStep() (*pb.SystemSnapshot, error) {
	if e.Snapshot.IsFinished {
		return e.Snapshot, errors.New("simulation already finished")
	}

	// 推进子步骤（EAGAIN 重试期间跳过推进，停留在当前子步骤）
	if !e.retrySubStep {
		e.SubStep++
	}
	e.retrySubStep = false
	e.Snapshot.SubStep = e.SubStep

	switch e.CurrentLayer {
	case pb.SystemSnapshot_LAYER_USER:
		e.executeUserLayer()
	case pb.SystemSnapshot_LAYER_INDEPENDENT:
		e.executeIndependentLayer()
	case pb.SystemSnapshot_LAYER_DRIVER:
		e.executeDriverLayer()
	case pb.SystemSnapshot_LAYER_HARDWARE:
		e.executeHardwareLayer()
	case pb.SystemSnapshot_LAYER_INTERRUPT:
		e.executeInterruptLayer()
	}

	e.StepIndex++
	return e.Snapshot, nil
}

// nextLayer 切换到下一层，重置子步骤计数器
func (e *SimulationEngine) nextLayer(next pb.SystemSnapshot_Layer) {
	e.CurrentLayer = next
	e.SubStep = 0
}

// =================================================================
//  L0 用户层 I/O 软件 (4 个子步骤)
// =================================================================

func (e *SimulationEngine) executeUserLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_USER
	e.Snapshot.TotalSubSteps = 4

	switch e.SubStep {
	case 1:
		// 子步骤 1: 标准 I/O 库函数调用 — fread() → libc → syscall wrapper
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【库函数调用】进程 %d 调用 fread(buf=0x%X, size=1, nmemb=%d, fp=0x%s) → "+
				"C 库 (libc) 从 FILE* 结构体中提取 fd=%d, 调用系统调用封装函数 read(fd=%d, buf, %d)",
			e.Snapshot.ProcessState.Pid, e.Config.UserBufferAddr,
			e.Config.BytesToRead, "7FFF1000", e.CurrentFd, e.CurrentFd, e.Config.BytesToRead)

	case 2:
		// 子步骤 2: 文件描述符表查找 — fd → file* → inode
		found := false
		for _, entry := range e.FdTable {
			if entry.Fd == e.CurrentFd {
				found = true
				if entry.Flags == 1 { // O_WRONLY -- 只写模式不可读
					e.Snapshot.StepDescription = fmt.Sprintf(
						"【fd 表查找 异常】fd=%d 对应文件 %q 以只写方式打开 (flags=O_WRONLY)，拒绝读取！",
						e.CurrentFd, entry.FilePath)
					e.Snapshot.FinalErrorCode = "EBADF (Bad file descriptor)"
					e.Snapshot.IsFinished = true
					return
				}
				e.Snapshot.StepDescription = fmt.Sprintf(
					"【fd 表查找】遍历进程打开文件表 fd_table[%d] → OpenFileEntry{path=%q, flags=O_RDONLY, pos=%d, %s} → "+
						"获取 file* → inode 指针。文件偏移量当前位于 %d 字节处。",
					e.CurrentFd, entry.FilePath, entry.FilePos, entry.InodePtr, entry.FilePos)
				break
			}
		}
		if !found {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【fd 表查找 异常】fd=%d 不在进程打开文件表中 — 文件描述符无效！进程 %d 未打开该文件。",
				e.CurrentFd, e.Snapshot.ProcessState.Pid)
			e.Snapshot.FinalErrorCode = "EBADF (Bad file descriptor)"
			e.Snapshot.IsFinished = true
			return
		}

	case 3:
		// 子步骤 3: 参数校验 — access_ok() 检查用户缓冲区地址合法性
		addr := e.Config.UserBufferAddr
		if e.InjectedFault == pb.FaultType_FAULT_INVALID_ADDRESS || addr >= 0xC0000000 {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【access_ok 异常】access_ok(buf=0x%X, len=%d) 失败！用户缓冲区地址 0x%X 超出用户地址空间 (TASK_SIZE=0xC0000000)，"+
					"可能写入内核空间。向进程发送 SIGSEGV 信号。",
				addr, e.Config.BytesToRead, addr)
			e.Snapshot.FinalErrorCode = "EFAULT (Bad address)"
			e.Snapshot.IsFinished = true
			return
		}
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【access_ok】验证用户缓冲区地址合法性: buf=0x%X 在 [0, TASK_SIZE) 范围内, len=%d。校验通过。",
			addr, e.Config.BytesToRead)

	case 4:
		// 子步骤 4: 陷入内核 — 触发系统调用，CPU 模式切换
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【陷入内核】系统调用参数装入寄存器: eax=NR_read(3), ebx=fd=%d, ecx=buf=0x%X, edx=len=%d。→ "+
				"执行 int 0x80 (x86) / sysenter 指令。CPU 硬件自动: ①压栈 SS/ESP/EFLAGS/CS/EIP ②从 MSR 加载内核 SS/ESP/CS/EIP "+
				"③切换到 Ring 0 (内核态)。跳转至内核 sys_read() 入口。",
			e.CurrentFd, e.Config.UserBufferAddr, e.Config.BytesToRead)
		e.nextLayer(pb.SystemSnapshot_LAYER_INDEPENDENT)
	}
}

// =================================================================
//  L1 设备无关软件 VFS (4 个子步骤)
// =================================================================

func (e *SimulationEngine) executeIndependentLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_INDEPENDENT
	e.Snapshot.TotalSubSteps = 5

	switch e.SubStep {
	case 1:
		// 子步骤 1: 路径解析 — path → dentry/inode
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【路径解析】VFS 将路径 %q 解析为 dentry → inode，遍历目录项缓存 (dcache)",
			e.Config.FilePath)

	case 2:
		// 子步骤 2: ACL 权限校验管线 (路径穿越→存在→敏感文件→权限位)
		if e.InjectedFault == pb.FaultType_FAULT_PERMISSION_DENIED {
			e.Snapshot.StepDescription = "【ACL 权限校验 异常】权限不足！当前用户无权读取该文件。"
			e.Snapshot.FinalErrorCode = "EACCES (Permission denied)"
			e.Snapshot.IsFinished = true
			return
		}
		if e.InjectedFault == pb.FaultType_FAULT_PATH_TRAVERSAL {
			e.Snapshot.StepDescription = "【ACL 权限校验 异常】路径穿越攻击！检测到 '../' 模式，试图逃逸用户主目录。"
			e.Snapshot.FinalErrorCode = "EPERM (Path traversal detected)"
			e.Snapshot.IsFinished = true
			return
		}
		if e.InjectedFault == pb.FaultType_FAULT_FILE_NOT_FOUND {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【ACL 权限校验 异常】文件 %q 在文件系统中不存在。", e.Config.FilePath)
			e.Snapshot.FinalErrorCode = "ENOENT (No such file or directory)"
			e.Snapshot.IsFinished = true
			return
		}

		// 真实文件访问校验管线
		if e.UserContext != nil {
			if valErr := ValidateFileAccess(e.UserContext, e.Config.FilePath); valErr != nil {
				if vErr, ok := valErr.(*ValidationError); ok {
					e.Snapshot.StepDescription = "【ACL 权限校验 异常】" + vErr.Description
					e.Snapshot.FinalErrorCode = vErr.Code
					e.Snapshot.IsFinished = true
					return
				}
			}
		}

		uid, gid := uint32(0), uint32(0)
		if e.UserContext != nil {
			uid = e.UserContext.Uid
			gid = e.UserContext.Gid
		}
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【ACL 权限校验】4 级管线通过：①路径穿越检测 ②文件存在检查 ③敏感文件检查 ④Unix 权限位 (UID=%d GID=%d) — 允许读取",
			uid, gid)

	case 3:
		// 子步骤 3: 页缓存查找 — 检查数据是否已在 Page Cache 中
		if e.Config.UsePageCache && e.PageCache != nil {
			if cachedData, ok := e.PageCache[e.Config.FilePath]; ok {
				// 缓存命中！直接返回数据，跳过磁盘 I/O
				e.Snapshot.MemoryState.CacheHit = true
				dataSize := int(e.Config.BytesToRead)
				if dataSize > len(cachedData) {
					dataSize = len(cachedData)
				}
				e.Snapshot.MemoryState.UserBufferData = cachedData[:dataSize]
				e.Snapshot.MemoryState.CachedPages = uint32(len(e.PageCache))
				e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_RUNNING
				e.Snapshot.ProcessState.WaitReason = ""

				// 更新文件偏移量
				bytesRead := int64(dataSize)
				for _, entry := range e.FdTable {
					if entry.Fd == e.CurrentFd {
						entry.FilePos += bytesRead
						break
					}
				}

				e.Snapshot.StepDescription = fmt.Sprintf(
					"【页缓存 命中 ✓】文件 %q 的 %d 字节数据已在页缓存 (Page Cache) 中！"+
						"直接从 RAM 拷贝到用户缓冲区，无需磁盘 I/O。"+
						"缓存页数: %d。sys_read() 返回 %d 字节。",
					e.Config.FilePath, dataSize, len(e.PageCache), bytesRead)
				e.Snapshot.FinalErrorCode = "SUCCESS"
				e.Snapshot.IsFinished = true
				return
			}
		}
		// 缓存未命中
		e.CacheMissed = e.Config.UsePageCache
		e.Snapshot.MemoryState.CacheHit = false
		if e.Config.UsePageCache {
			cachePages := uint32(0)
			if e.PageCache != nil {
				cachePages = uint32(len(e.PageCache))
			}
			e.Snapshot.MemoryState.CachedPages = cachePages
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【页缓存 未命中 ✗】文件 %q 不在页缓存中，需要访问磁盘。"+
					"VFS 向 Page Cache 分配新页框，准备从磁盘填充。当前缓存 %d 页。",
				e.Config.FilePath, cachePages)
		} else {
			e.Snapshot.StepDescription = "【直接 I/O】页缓存已禁用，直接访问磁盘。"
		}

	case 4:
		// 子步骤 4: 内核缓冲分配
		bufType := "单缓冲区 (Simple Buffer)"
		if e.Config.UseDoubleBuffer {
			bufType = fmt.Sprintf("双缓冲区 / 乒乓缓冲 (Ping-Pong A/B, %d 块 × %d 字节)", e.TotalChunks, chunkSize)
		}
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【缓冲分配】VFS 根据配置在内核空间分配 %s，准备接收磁盘数据", bufType)

	case 5:
		// 子步骤 5: IRP 构造与下发
		e.Snapshot.MemoryState.CurrentIrpInfo = fmt.Sprintf(
			"IRP_READ → Dev:Disk0, Len:%d, Buf:0x%X", e.Config.BytesToRead, e.Config.UserBufferAddr)
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【IRP 构造】构造 I/O 请求包 (IRP): %s。IRP 沿设备栈向下提交至驱动程序。",
			e.Snapshot.MemoryState.CurrentIrpInfo)
		e.nextLayer(pb.SystemSnapshot_LAYER_DRIVER)
	}
}

// =================================================================
//  L2 设备驱动程序 (3 个子步骤)
// =================================================================

func (e *SimulationEngine) executeDriverLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_DRIVER
	e.Snapshot.TotalSubSteps = 3

	switch e.SubStep {
	case 1:
		// 子步骤 1: IRP 解析与 I/O 策略选择
		if e.Config.UseDoubleBuffer {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【IRP 解析】驱动程序提取 IRP 参数：操作=READ, 设备=Disk0, 数据块 %d/%d, 长度=%d",
				e.CurrentChunk+1, e.TotalChunks, chunkSize)
		} else {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【IRP 解析】驱动程序提取 IRP 参数：操作=READ, 设备=Disk0, 长度=%d 字节",
				e.Config.BytesToRead)
		}

	case 2:
		// 子步骤 2: 设备寄存器编程 (Memory-Mapped I/O)

		// EAGAIN 可恢复错误：设备忙，重试
		if e.InjectedFault == pb.FaultType_FAULT_EAGAIN && e.RetryCount < e.RetryMax {
			e.RetryCount++
			e.Snapshot.MemoryState.RetryCount = e.RetryCount
			e.Snapshot.MemoryState.RetryMax = e.RetryMax
			e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY (EAGAIN)"
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【设备忙 重试 %d/%d】EAGAIN — 设备控制器暂时不可用。"+
					"驱动程序重试写入设备寄存器。点击「▶ 下一步」继续重试。",
				e.RetryCount, e.RetryMax)
			e.retrySubStep = true // 下次 NextStep 跳过 SubStep++，停留在子步骤 2
			return
		}
		// 重试耗尽：设备就绪，继续正常编程
		eagerRetrySucceeded := e.InjectedFault == pb.FaultType_FAULT_EAGAIN && e.RetryCount >= e.RetryMax
		if eagerRetrySucceeded {
			e.RetryCount = 0
			e.Snapshot.MemoryState.RetryCount = 0 // 清除前端重试计数
		}

		if e.Config.UseDoubleBuffer {
			remainingBytes := e.Config.BytesToRead - uint32(e.CurrentChunk)*chunkSize
			if remainingBytes > chunkSize {
				remainingBytes = chunkSize
			}
			e.Snapshot.HardwareState.CmdRegister = fmt.Sprintf(
				"0x01: READ_SECTOR (Chunk %d/%d, DMA→Buf%d, %d bytes)",
				e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer, remainingBytes)
		} else {
			e.Snapshot.HardwareState.CmdRegister = "0x01: READ_SECTOR"
		}
		e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY"
		if eagerRetrySucceeded {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【重试成功 → 寄存器编程】%d 次重试后设备就绪。驱动程序写入：CMD_REG=%s, STS_REG=DEVICE_BUSY",
				e.RetryMax, e.Snapshot.HardwareState.CmdRegister)
		} else {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【寄存器编程】驱动程序通过内存映射 I/O 写入设备寄存器：CMD_REG=%s, STS_REG=DEVICE_BUSY",
				e.Snapshot.HardwareState.CmdRegister)
		}

	case 3:
		// 子步骤 3: 进程阻塞，等待 I/O 完成
		e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_BLOCKED
		e.Snapshot.ProcessState.WaitReason = "等待磁盘 I/O 完成 (DMA 传输 + 硬件中断)"
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【进程阻塞】进程 %d 进入 BLOCKED 状态，CPU 调度器切换到其他就绪进程。"+
				"当前进程等待硬件中断唤醒。",
			e.Snapshot.ProcessState.Pid)
		e.nextLayer(pb.SystemSnapshot_LAYER_HARDWARE)
	}
}

// =================================================================
//  L4 硬件层 (磁盘控制器) (3 个子步骤)
// =================================================================

func (e *SimulationEngine) executeHardwareLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_HARDWARE
	e.Snapshot.TotalSubSteps = 3

	switch e.SubStep {
	case 1:
		// 子步骤 1: DMA 控制器初始化
		var dmaBytes uint32
		if e.Config.UseDoubleBuffer {
			dmaBytes = e.Config.BytesToRead - uint32(e.CurrentChunk)*chunkSize
			if dmaBytes > chunkSize {
				dmaBytes = chunkSize
			}
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【DMA 启动】磁盘控制器初始化 DMA 传输：源=磁盘扇区, 目标=内核缓冲区 %d, 字节数=%d。"+
					"DMA 控制器接管总线，CPU 可继续执行其他任务。",
				e.ActiveWriteBuffer, dmaBytes)
		} else {
			dmaBytes = e.Config.BytesToRead
			e.Snapshot.StepDescription = "【DMA 启动】磁盘控制器初始化 DMA 传输：源=磁盘扇区, 目标=内核缓冲区。" +
				"DMA 控制器接管总线，CPU 可继续执行其他任务。"
		}
		// 编程 DMA 控制器寄存器
		e.Snapshot.HardwareState.DmaSource = fmt.Sprintf("0x%04X: Disk0 Sector", 0x400+e.CurrentChunk*8)
		e.Snapshot.HardwareState.DmaDestination = fmt.Sprintf("0xBEEF%04X: Kernel Buf%d",
			0x1000*e.ActiveWriteBuffer, e.ActiveWriteBuffer)
		e.Snapshot.HardwareState.DmaCount = dmaBytes
		e.Snapshot.HardwareState.DmaStatus = "SETUP"

	case 2:
		// 子步骤 2: 磁盘读取 + 数据就绪
		if e.InjectedFault == pb.FaultType_FAULT_HARDWARE_TIMEOUT {
			e.Snapshot.HardwareState.StatusRegister = "0x03: DEVICE_ERROR"
			e.Snapshot.HardwareState.DmaStatus = "ERROR"
			e.Snapshot.StepDescription = "【磁盘读取 异常】硬件响应超时！磁道损坏或设备控制器掉线。"
			e.Snapshot.FinalErrorCode = "EIO (Input/output error)"
			e.Snapshot.IsFinished = true
			e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_READY
			return
		}

		// DMA 传输进行中
		e.Snapshot.HardwareState.DmaStatus = "TRANSFERRING"

		// 生成模拟数据
		if e.Config.UseDoubleBuffer {
			actualSize := e.Config.BytesToRead - uint32(e.CurrentChunk)*chunkSize
			if actualSize > chunkSize {
				actualSize = chunkSize
			}
			chunkPrefix := fmt.Sprintf("[CHUNK_%d_START]", e.CurrentChunk)
			chunkBody := fmt.Sprintf("OS_DATA_BLOCK_%d", e.CurrentChunk)
			chunkData := chunkPrefix + chunkBody
			for len(chunkData) < int(actualSize) {
				chunkData += fmt.Sprintf("_%X", len(chunkData))
			}
			e.Snapshot.HardwareState.DataRegister = []byte(chunkData[:actualSize])
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【磁盘读取】磁头寻道完成，扇区数据 (%d 字节) 通过 DMA 传输至内核缓冲区 %d。"+
					"数据已写入设备数据寄存器 DATA_REG。",
				actualSize, e.ActiveWriteBuffer)
		} else {
			// 单缓冲模式：生成 bytesToRead 字节的模拟数据
			dataSize := e.Config.BytesToRead
			data := "[SINGLE_BUF]OS_READ_DATA"
			for len(data) < int(dataSize) {
				data += fmt.Sprintf("_%X", len(data))
			}
			e.Snapshot.HardwareState.DataRegister = []byte(data[:dataSize])
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【磁盘读取】磁头寻道完成，扇区数据 (%d 字节) 通过 DMA 传输至内核缓冲区。数据已写入 DATA_REG。",
				dataSize)
		}

		e.Snapshot.HardwareState.DmaStatus = "DONE"
		e.Snapshot.HardwareState.StatusRegister = "0x04: DATA_READY"

	case 3:
		// 子步骤 3: 硬件中断发出
		e.Snapshot.StepDescription = "【硬件中断】磁盘控制器通过 IRQ 线向 CPU 发出中断请求 (IRQ#14)。" +
			"中断控制器 (PIC/APIC) 将中断向量号传递给 CPU，触发中断处理流程。"
		e.nextLayer(pb.SystemSnapshot_LAYER_INTERRUPT)
	}
}

// =================================================================
//  L3 中断处理程序 ISR (3 个子步骤)
// =================================================================

func (e *SimulationEngine) executeInterruptLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_INTERRUPT
	e.Snapshot.TotalSubSteps = 3

	switch e.SubStep {
	case 1:
		// 子步骤 1: ISR 接管 — 保存上下文，识别中断源
		e.Snapshot.StepDescription = "【ISR 接管】CPU 响应 IRQ#14，硬件自动压栈 (EIP/CS/EFLAGS)。" +
			"中断处理程序 (ISR) 入口：保存寄存器上下文，识别中断源为磁盘控制器。"

	case 2:
		// 子步骤 2: 数据拷贝 — 硬件寄存器 → 内核缓冲区 → 用户缓冲区
		hwData := e.Snapshot.HardwareState.DataRegister
		if e.ActiveWriteBuffer == 1 {
			e.Snapshot.MemoryState.KernelBuffer_1Data = hwData
		} else {
			e.Snapshot.MemoryState.KernelBuffer_2Data = hwData
		}

		// 清空硬件数据寄存器
		e.Snapshot.HardwareState.DataRegister = []byte{}
		e.Snapshot.HardwareState.StatusRegister = "0x01: READY"

		// 内核缓冲区 → 用户缓冲区 (copy_to_user)
		if e.ActiveWriteBuffer == 1 {
			e.Snapshot.MemoryState.UserBufferData = append(
				e.Snapshot.MemoryState.UserBufferData,
				e.Snapshot.MemoryState.KernelBuffer_1Data...)
		} else {
			e.Snapshot.MemoryState.UserBufferData = append(
				e.Snapshot.MemoryState.UserBufferData,
				e.Snapshot.MemoryState.KernelBuffer_2Data...)
		}

		// 更新追踪字段
		e.Snapshot.MemoryState.ActiveWriteBuffer = e.ActiveWriteBuffer
		e.Snapshot.MemoryState.ActiveReadBuffer = e.ActiveReadBuffer
		e.Snapshot.MemoryState.CurrentChunk = e.CurrentChunk
		e.Snapshot.MemoryState.TotalChunks = e.TotalChunks

		e.Snapshot.StepDescription = fmt.Sprintf(
			"【数据拷贝】ISR 执行: ①硬件数据寄存器 → 内核缓冲区 %d (%d 字节) ②copy_to_user() → 用户缓冲区 0x%X。"+
				"累计已接收 %d 字节。",
			e.ActiveWriteBuffer, len(hwData), e.Config.UserBufferAddr,
			len(e.Snapshot.MemoryState.UserBufferData))

	case 3:
		// 子步骤 3: 判断是否还有更多数据块
		if e.Config.UseDoubleBuffer && e.CurrentChunk+1 < e.TotalChunks {
			e.CurrentChunk++

			// 乒乓切换
			oldWriteBuffer := e.ActiveWriteBuffer
			oldReadBuffer := e.ActiveReadBuffer
			e.ActiveReadBuffer = e.ActiveWriteBuffer
			if e.ActiveWriteBuffer == 1 {
				e.ActiveWriteBuffer = 2
			} else {
				e.ActiveWriteBuffer = 1
			}

			// 编程下一次 DMA
			e.Snapshot.HardwareState.CmdRegister = fmt.Sprintf(
				"0x01: READ_SECTOR (Chunk %d/%d, DMA→Buf%d)",
				e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer)
			e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY"
			e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_BLOCKED
			e.Snapshot.ProcessState.WaitReason = fmt.Sprintf(
				"等待数据块 %d/%d 通过 DMA 传输至缓冲区 %d",
				e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer)

			e.Snapshot.StepDescription = fmt.Sprintf(
				"【乒乓切换】数据块 %d/%d 已送达用户空间。"+
					"缓冲区切换: 写入目标 Buf%d → Buf%d, 读取源 Buf%d → Buf%d。"+
					"已编程下一块 DMA (Chunk %d → Buf%d)。进程再次阻塞等待。",
				e.CurrentChunk, e.TotalChunks,
				oldWriteBuffer, e.ActiveWriteBuffer, oldReadBuffer, e.ActiveReadBuffer,
				e.CurrentChunk+1, e.ActiveWriteBuffer)

			// 回到驱动层，继续下一轮 DMA 周期
			e.nextLayer(pb.SystemSnapshot_LAYER_DRIVER)
		} else {
			// 所有数据块读取完毕
			e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_RUNNING
			e.Snapshot.ProcessState.WaitReason = ""

			// 更新文件偏移量 (模拟 lseek 行为)
			bytesRead := int64(len(e.Snapshot.MemoryState.UserBufferData))
			var oldPos, newPos int64
			for _, entry := range e.FdTable {
				if entry.Fd == e.CurrentFd {
					oldPos = entry.FilePos
					entry.FilePos += bytesRead
					newPos = entry.FilePos
					break
				}
			}

			if e.Config.UseDoubleBuffer {
				e.Snapshot.StepDescription = fmt.Sprintf(
					"【I/O 完成】全部 %d 个数据块通过乒乓缓冲机制送达用户缓冲区。"+
						"sys_read() 返回 %d 字节。文件偏移量: %d → %d。"+
						"ISR 恢复上下文 (iret)，进程 %d 回到用户态继续执行。",
					e.TotalChunks, bytesRead, oldPos, newPos,
					e.Snapshot.ProcessState.Pid)
			} else {
				e.Snapshot.StepDescription = fmt.Sprintf(
					"【I/O 完成】数据已送达用户缓冲区。sys_read() 返回 %d 字节。"+
						"文件偏移量: %d → %d。"+
						"ISR 恢复上下文 (iret)，进程 %d 回到用户态继续执行。",
					bytesRead, oldPos, newPos, e.Snapshot.ProcessState.Pid)
			}

			// 页缓存回写：缓存未命中时，将读取的数据存入 Page Cache
			if e.CacheMissed && e.PageCache != nil {
				e.PageCache[e.Config.FilePath] = e.Snapshot.MemoryState.UserBufferData
				e.Snapshot.MemoryState.CachedPages = uint32(len(e.PageCache))
				e.CacheMissed = false
			}

			e.Snapshot.IsFinished = true
		}
	}
}
