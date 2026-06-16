package engine

import (
	"errors"
	"fmt"
	"io-simulator/api/pb" // 注意：如果你的 go.mod 里 module 名字不是 io-simulator，请修改此处
)

// SimulationEngine 掌控全局状态机
type SimulationEngine struct {
	CurrentLayer  pb.SystemSnapshot_Layer
	StepIndex     int
	Config        *pb.ReadRequestConfig
	Snapshot      *pb.SystemSnapshot
	InjectedFault pb.FaultType // 👉 将故障状态直接存在引擎上
}

// NewEngine 初始化一次全新的 read 模拟
func NewEngine(config *pb.ReadRequestConfig) *SimulationEngine {
	// 默认给一个模拟的 PID 和初始状态
	return &SimulationEngine{
		CurrentLayer:  pb.SystemSnapshot_LAYER_USER,
		StepIndex:     0,
		Config:        config,
		InjectedFault: pb.FaultType_FAULT_NONE, // 默认无故障
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

	// 异常注入测试 1：模拟权限不足 (已修改为 e.InjectedFault)
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

	// 正常流：分配缓冲区并构造 IRP
	bufType := "单缓冲区"
	if e.Config.UseDoubleBuffer {
		bufType = "双缓冲区"
	}
	e.Snapshot.MemoryState.CurrentIrpInfo = fmt.Sprintf("IRP_READ -> Dev:Disk0, Len:%d", e.Config.BytesToRead)
	e.Snapshot.StepDescription = fmt.Sprintf("【设备无关层】校验通过。系统分配了【%s】。已构造 I/O 请求包 (IRP) 下发给驱动程序。", bufType)

	e.CurrentLayer = pb.SystemSnapshot_LAYER_DRIVER
}

func (e *SimulationEngine) executeDriverLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_DRIVER
	e.Snapshot.StepDescription = "【设备驱动层】收到 IRP。驱动程序开始解析并将物理请求写入硬件控制寄存器。随后阻塞当前进程..."

	// 操作寄存器
	e.Snapshot.HardwareState.CmdRegister = "0x01: READ_SECTOR"
	e.Snapshot.HardwareState.StatusRegister = "0x02: DEVICE_BUSY"

	// 进程阻塞挂起
	e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_BLOCKED
	e.Snapshot.ProcessState.WaitReason = "等待物理硬件 I/O 中断信号"

	e.CurrentLayer = pb.SystemSnapshot_LAYER_HARDWARE
}

func (e *SimulationEngine) executeHardwareLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_HARDWARE
	e.Snapshot.StepDescription = "【模拟硬件】磁盘磁头开始寻道并读取扇区数据..."

	// 异常注入测试 3：硬件超时故障 (已修改为 e.InjectedFault)
	if e.InjectedFault == pb.FaultType_FAULT_HARDWARE_TIMEOUT {
		e.Snapshot.StepDescription = "【模拟硬件 异常】硬件响应超时！磁道损坏或设备掉线。"
		e.Snapshot.HardwareState.StatusRegister = "0x03: DEVICE_ERROR"
		e.Snapshot.FinalErrorCode = "EIO (Input/output error)"
		e.Snapshot.IsFinished = true
		// 即使出错也得唤醒进程处理善后
		e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_READY
		return
	}

	// 正常流：硬件读取数据成功放入数据寄存器
	e.Snapshot.HardwareState.DataRegister = []byte("OS_DATA_STREAM")
	e.Snapshot.HardwareState.StatusRegister = "0x04: DATA_READY"
	e.Snapshot.StepDescription = "【模拟硬件】物理读取完毕，数据已载入设备数据寄存器。正在向 CPU 发出硬件中断信号！"

	e.CurrentLayer = pb.SystemSnapshot_LAYER_INTERRUPT
}

func (e *SimulationEngine) executeInterruptLayer() {
	e.Snapshot.CurrentActiveLayer = pb.SystemSnapshot_LAYER_INTERRUPT
	e.Snapshot.StepDescription = "【中断处理层】CPU 响应中断。中断处理程序接管，开始将数据从硬件寄存器搬运到内核缓冲区..."

	// 模拟第一次拷贝：硬件 -> 内核缓冲区
	e.Snapshot.MemoryState.KernelBuffer_1Data = e.Snapshot.HardwareState.DataRegister
	e.Snapshot.HardwareState.DataRegister = []byte{} // 清空寄存器
	e.Snapshot.HardwareState.StatusRegister = "0x01: READY"
	e.Snapshot.StepDescription = "【中断处理层】数据已成功拷贝至内核缓冲区。正在将数据二次拷贝至用户空间缓冲区，并唤醒用户进程..."

	// 模拟第二次拷贝：内核缓冲区 -> 用户缓冲区 (copy_to_user)
	e.Snapshot.MemoryState.UserBufferData = e.Snapshot.MemoryState.KernelBuffer_1Data

	// 唤醒进程
	e.Snapshot.ProcessState.State = pb.ProcessBlock_STATE_RUNNING
	e.Snapshot.ProcessState.WaitReason = ""

	e.Snapshot.StepDescription = "【I/O 结束】数据顺利送达用户缓冲区。`read` 系统调用成功返回，用户进程继续执行！"
	e.Snapshot.IsFinished = true
}