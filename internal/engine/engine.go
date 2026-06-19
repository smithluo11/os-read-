package engine

import (
	"errors"
	"fmt"
	"io-simulator/api/pb" 
)

const chunkSize = 4096 // 4KB per chunk (模拟磁盘扇区/页大小)

// SimulationEngine 掌控全局状态机
type SimulationEngine struct {
	CurrentLayer  pb.SystemSnapshot_Layer
	StepIndex     int
	Config        *pb.ReadRequestConfig
	Snapshot      *pb.SystemSnapshot
	InjectedFault pb.FaultType
	UserContext   *pb.UserContext // 当前用户上下文

	// 双缓冲 / 乒乓缓冲状态
	ActiveWriteBuffer int32 // 硬件 DMA 目标缓冲区 (1 或 2)
	ActiveReadBuffer  int32 // CPU 拷贝源缓冲区 (1 或 2)
	CurrentChunk      int32 // 当前数据块序号 (0-based)
	TotalChunks       int32 // 总数据块数
}

// NewEngine 初始化一次全新的 read 模拟
func NewEngine(config *pb.ReadRequestConfig, userCtx *pb.UserContext) *SimulationEngine {
	// 计算总数据块数：双缓冲时按 chunkSize 切分，单缓冲时 totalChunks=1
	totalChunks := int32(1)
	if config.UseDoubleBuffer && config.BytesToRead > 0 {
		totalChunks = int32((config.BytesToRead + chunkSize - 1) / chunkSize)
	}

	// 默认给一个模拟的 PID 和初始状态
	return &SimulationEngine{
		CurrentLayer:  pb.SystemSnapshot_LAYER_USER,
		StepIndex:     0,
		Config:        config,
		InjectedFault: pb.FaultType_FAULT_NONE,
		UserContext:   userCtx,

		ActiveWriteBuffer: 1,
		ActiveReadBuffer:  1,
		CurrentChunk:      0,
		TotalChunks:       totalChunks,

		Snapshot: &pb.SystemSnapshot{
			CurrentActiveLayer: pb.SystemSnapshot_LAYER_USER,
			StepDescription:    "等待用户发起 read() 系统调用...",
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
			},
			HardwareState: &pb.HardwareView{
				CmdRegister:    "0x00: NO_OP",
				StatusRegister: "0x01: READY",
				DataRegister:   []byte{},
			},
			IsFinished:     false,
			FinalErrorCode: "SUCCESS",
		},
	}
}

// NextStep 核心驱动：前端每点一次“下一步”，这个函数执行一次
func (e *SimulationEngine) NextStep() (*pb.SystemSnapshot, error) {
	if e.Snapshot.IsFinished {
		return e.Snapshot, errors.New("simulation already finished")
	}

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

// =================================================================
//  下面是各层执行逻辑 —— 以后分给队友改的就是这里面
// =================================================================

func (e *SimulationEngine) executeUserLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_USER
	e.Snapshot.StepDescription = fmt.Sprintf("【用户层】进程 %d 调用 read() 库函数。尝试读取文件：%s，长度：%d 字节。开始陷入内核...", e.Snapshot.ProcessState.Pid, e.Config.FilePath, e.Config.BytesToRead)

	// 状态迁移：下一步去内核设备无关层
	e.CurrentLayer = pb.SystemSnapshot_LAYER_INDEPENDENT
}

func (e *SimulationEngine) executeIndependentLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_INDEPENDENT
	e.Snapshot.StepDescription = "【设备无关层】内核捕获系统调用。正在进行安全校验..."

	// 异常注入测试 1：模拟权限不足
	if e.InjectedFault == pb.FaultType_FAULT_PERMISSION_DENIED {
		e.Snapshot.StepDescription = "【设备无关层 异常】权限校验失败！当前用户无权读取该模拟敏感文件。"
		e.Snapshot.FinalErrorCode = "EACCES (Permission denied)"
		e.Snapshot.IsFinished = true
		return
	}

	// 异常注入测试 2：模拟非法地址
	if e.Config.UserBufferAddr > 0x7FFFFFFF {
		e.Snapshot.StepDescription = "【设备无关层 异常】段错误！用户传入的缓冲区地址 0x" + fmt.Sprintf("%X", e.Config.UserBufferAddr) + " 越界。"
		e.Snapshot.FinalErrorCode = "EFAULT (Bad address)"
		e.Snapshot.IsFinished = true
		return
	}

	// 异常注入测试 3：模拟路径穿越攻击
	if e.InjectedFault == pb.FaultType_FAULT_PATH_TRAVERSAL {
		e.Snapshot.StepDescription = "【设备无关层 异常】路径遍历攻击！检测到 '../' 模式，试图访问用户主目录之外的文件。"
		e.Snapshot.FinalErrorCode = "EFAULT (Path traversal detected)"
		e.Snapshot.IsFinished = true
		return
	}

	// 异常注入测试 4：模拟文件不存在
	if e.InjectedFault == pb.FaultType_FAULT_FILE_NOT_FOUND {
		e.Snapshot.StepDescription = "【设备无关层 异常】文件未找到！路径：'" + e.Config.FilePath + "' 在文件系统中不存在。"
		e.Snapshot.FinalErrorCode = "ENOENT (No such file or directory)"
		e.Snapshot.IsFinished = true
		return
	}

	// 真实文件访问校验管线：路径穿越 → 存在检查 → 敏感文件 → 权限位
	if e.UserContext != nil {
		if valErr := ValidateFileAccess(e.UserContext, e.Config.FilePath); valErr != nil {
			if vErr, ok := valErr.(*ValidationError); ok {
				e.Snapshot.StepDescription = "【设备无关层 异常】" + vErr.Description
				e.Snapshot.FinalErrorCode = vErr.Code
				e.Snapshot.IsFinished = true
				return
			}
		}
	}

	// 正常流：分配缓冲区并构造 IRP
	bufType := "单缓冲区"
	if e.Config.UseDoubleBuffer {
		bufType = fmt.Sprintf("双缓冲区 (%d 个数据块, 每块 %d 字节)", e.TotalChunks, chunkSize)
	}
	e.Snapshot.MemoryState.CurrentIrpInfo = fmt.Sprintf("IRP_READ -> Dev:Disk0, Len:%d", e.Config.BytesToRead)
	e.Snapshot.StepDescription = fmt.Sprintf("【设备无关层】校验通过。系统分配了【%s】。已构造 I/O 请求包 (IRP) 下发给驱动程序。", bufType)

	e.CurrentLayer = pb.SystemSnapshot_LAYER_DRIVER
}

func (e *SimulationEngine) executeDriverLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_DRIVER

	// 双缓冲模式：显示当前数据块和 DMA 目标缓冲区
	if e.Config.UseDoubleBuffer {
		remainingBytes := e.Config.BytesToRead - uint32(e.CurrentChunk)*chunkSize
		if remainingBytes > chunkSize {
			remainingBytes = chunkSize
		}
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【设备驱动层】收到 IRP。驱动程序开始将数据块 %d/%d 写入控制寄存器 (DMA → 内核缓冲区 %d, %d 字节)。随后阻塞当前进程...",
			e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer, remainingBytes)
		e.Snapshot.HardwareState.CmdRegister = fmt.Sprintf(
			"0x01: READ_SECTOR (Chunk %d/%d, DMA→Buf%d)",
			e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer)
	} else {
		e.Snapshot.StepDescription = "【设备驱动层】收到 IRP。驱动程序开始解析并将物理请求写入硬件控制寄存器。随后阻塞当前进程..."
		e.Snapshot.HardwareState.CmdRegister = "0x01: READ_SECTOR"
	}

	e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY"
	e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_BLOCKED
	e.Snapshot.ProcessState.WaitReason = "等待物理硬件 I/O 中断信号"

	e.CurrentLayer = pb.SystemSnapshot_LAYER_HARDWARE
}

func (e *SimulationEngine) executeHardwareLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_HARDWARE
	e.Snapshot.StepDescription = "【模拟硬件】磁盘磁头开始寻道并读取扇区数据..."

	// 异常注入：硬件超时故障
	if e.InjectedFault == pb.FaultType_FAULT_HARDWARE_TIMEOUT {
		e.Snapshot.StepDescription = "【模拟硬件 异常】硬件响应超时！磁道损坏或设备掉线。"
		e.Snapshot.HardwareState.StatusRegister = "0x03: DEVICE_ERROR"
		e.Snapshot.FinalErrorCode = "EIO (Input/output error)"
		e.Snapshot.IsFinished = true
		e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_READY
		return
	}

	// 生成数据：双缓冲模式按块序号动态生成，单缓冲模式使用统一数据流
	if e.Config.UseDoubleBuffer {
		// 动态生成带块序号的数据，便于前端观察用户缓冲区逐步累加
		actualSize := e.Config.BytesToRead - uint32(e.CurrentChunk)*chunkSize
		if actualSize > chunkSize {
			actualSize = chunkSize
		}
		chunkPrefix := fmt.Sprintf("[CHUNK_%d_START]", e.CurrentChunk)
		chunkBody := fmt.Sprintf("OS_DATA_BLOCK_%d", e.CurrentChunk)
		chunkData := chunkPrefix + chunkBody
		// 填充到接近实际块大小，以便前端看到可观测的数据差异
		for len(chunkData) < int(actualSize) {
			chunkData += fmt.Sprintf("_%X", len(chunkData))
		}
		e.Snapshot.HardwareState.DataRegister = []byte(chunkData[:actualSize])
		e.Snapshot.StepDescription = fmt.Sprintf(
			"【模拟硬件】物理读取数据块 %d/%d 完毕（%d 字节），数据已载入设备数据寄存器。"+
				"正在进行 DMA 传输至内核缓冲区 %d。向 CPU 发出硬件中断信号！",
			e.CurrentChunk+1, e.TotalChunks, actualSize, e.ActiveWriteBuffer)
	} else {
		e.Snapshot.HardwareState.DataRegister = []byte("OS_DATA_STREAM")
		e.Snapshot.StepDescription = "【模拟硬件】物理读取完毕，数据已载入设备数据寄存器。正在向 CPU 发出硬件中断信号！"
	}

	e.Snapshot.HardwareState.StatusRegister = "0x04: DATA_READY"
	e.CurrentLayer = pb.SystemSnapshot_LAYER_INTERRUPT
}

func (e *SimulationEngine) executeInterruptLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_INTERRUPT
	e.Snapshot.StepDescription = "【中断处理层】CPU 响应中断。中断处理程序接管，开始将数据从硬件寄存器搬运到内核缓冲区..."

	// Step 1: 硬件数据寄存器 → 内核缓冲区 (DMA 完成，数据到达目标缓冲区)
	hwData := e.Snapshot.HardwareState.DataRegister
	if e.ActiveWriteBuffer == 1 {
		e.Snapshot.MemoryState.KernelBuffer_1Data = hwData
	} else {
		e.Snapshot.MemoryState.KernelBuffer_2Data = hwData
	}

	// 清空硬件数据寄存器
	e.Snapshot.HardwareState.DataRegister = []byte{}
	e.Snapshot.HardwareState.StatusRegister = "0x01: READY"

	// Step 2: 内核缓冲区 → 用户缓冲区 (copy_to_user，累加拼接)
	if e.ActiveWriteBuffer == 1 {
		e.Snapshot.MemoryState.UserBufferData = append(
			e.Snapshot.MemoryState.UserBufferData,
			e.Snapshot.MemoryState.KernelBuffer_1Data...)
	} else {
		e.Snapshot.MemoryState.UserBufferData = append(
			e.Snapshot.MemoryState.UserBufferData,
			e.Snapshot.MemoryState.KernelBuffer_2Data...)
	}

	// 更新 MemoryView 追踪字段，供前端可视化
	e.Snapshot.MemoryState.ActiveWriteBuffer = e.ActiveWriteBuffer
	e.Snapshot.MemoryState.ActiveReadBuffer = e.ActiveReadBuffer
	e.Snapshot.MemoryState.CurrentChunk = e.CurrentChunk
	e.Snapshot.MemoryState.TotalChunks = e.TotalChunks

	// Step 3: 判断是否还有更多数据块需要读取
	if e.Config.UseDoubleBuffer && e.CurrentChunk+1 < e.TotalChunks {
		// 推进块计数器
		e.CurrentChunk++

		// 乒乓切换：刚写入的缓冲区变为"读取源"，另一个缓冲区变为"DMA 目标"
		e.ActiveReadBuffer = e.ActiveWriteBuffer
		if e.ActiveWriteBuffer == 1 {
			e.ActiveWriteBuffer = 2
		} else {
			e.ActiveWriteBuffer = 1
		}

		// 编程下一次 DMA（中断处理层同时完成数据拷贝和新 DMA 调度）
		e.Snapshot.HardwareState.CmdRegister = fmt.Sprintf(
			"0x01: READ_SECTOR (Chunk %d/%d, DMA→Buf%d)",
			e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer)
		e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY"
		e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_BLOCKED
		e.Snapshot.ProcessState.WaitReason = fmt.Sprintf(
			"等待数据块 %d/%d 通过 DMA 传输至缓冲区 %d",
			e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer)

		e.Snapshot.StepDescription = fmt.Sprintf(
			"【中断处理层 乒乓切换】数据块 %d/%d 已从内核缓冲区 %d 拷贝至用户空间。同时已对数据块 %d/%d 编程 DMA → 缓冲区 %d。"+
				"硬件正在并行写入缓冲区 %d，CPU 下次将从缓冲区 %d 读取。",
			e.CurrentChunk, e.TotalChunks, e.ActiveReadBuffer,
			e.CurrentChunk+1, e.TotalChunks, e.ActiveWriteBuffer,
			e.ActiveWriteBuffer, e.ActiveReadBuffer)

		// 回到设备驱动层，继续下一轮的 DMA 周期
		e.CurrentLayer = pb.SystemSnapshot_LAYER_DRIVER
	} else {
		// 所有数据块读取完毕
		e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_RUNNING
		e.Snapshot.ProcessState.WaitReason = ""

		if e.Config.UseDoubleBuffer {
			e.Snapshot.StepDescription = fmt.Sprintf(
				"【I/O 结束】所有 %d 个数据块已通过乒乓缓冲机制顺利送达用户缓冲区。"+
					"`read` 系统调用成功返回 %d 字节，用户进程继续执行！",
				e.TotalChunks, len(e.Snapshot.MemoryState.UserBufferData))
		} else {
			e.Snapshot.StepDescription = "【I/O 结束】数据顺利送达用户缓冲区。`read` 系统调用成功返回，用户进程继续执行！"
		}

		e.Snapshot.IsFinished = true
	}
}