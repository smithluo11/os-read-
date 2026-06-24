package engine

import (
	"fmt"
	"io-simulator/api/pb"
	"testing"
)

func TestSubStepFlow_SingleBuffer(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    16384,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	userCtx := &pb.UserContext{Uid: 1000, Gid: 1000, Username: "user1", HomeDir: "/home/user1"}
	eng := NewEngine(config, userCtx)

	layerNames := map[int32]string{0: "USER", 1: "VFS", 2: "DRV", 3: "INT", 4: "HW"}

	// Track sub-step transitions per layer
	layerStepCounts := map[string]int{}
	prevLayer := ""
	step := 0

	for !eng.Snapshot.IsFinished && step < 100 {
		snap, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
		layerName := layerNames[int32(snap.CurrentActiveLayer)]

		if layerName != prevLayer {
			layerStepCounts[layerName] = 1
			prevLayer = layerName
		} else {
			layerStepCounts[layerName]++
		}

		// Verify sub_step is within valid range (nextLayer resets eng.SubStep=0
		// after the snapshot is captured, so snap.SubStep can differ for the last step)
		if snap.SubStep < 1 || snap.SubStep > snap.TotalSubSteps {
			t.Errorf("Step %d: snap.SubStep=%d out of range [1,%d]",
				step, snap.SubStep, snap.TotalSubSteps)
		}
	}

	fmt.Printf("单缓冲测试: %d 步完成, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	fmt.Printf("各层子步骤数: USER=%d, VFS=%d, DRV=%d, HW=%d, INT=%d\n",
		layerStepCounts["USER"], layerStepCounts["VFS"], layerStepCounts["DRV"],
		layerStepCounts["HW"], layerStepCounts["INT"])

	// Verify expected sub-step counts
	if layerStepCounts["USER"] != 4 {
		t.Errorf("USER layer: expected 4 sub-steps, got %d", layerStepCounts["USER"])
	}
	if layerStepCounts["VFS"] != 5 {
		t.Errorf("VFS layer: expected 5 sub-steps, got %d", layerStepCounts["VFS"])
	}
	if layerStepCounts["DRV"] != 3 {
		t.Errorf("DRV layer: expected 3 sub-steps, got %d", layerStepCounts["DRV"])
	}

	// Verify file offset was updated (single-buffer now generates BytesToRead bytes)
	if eng.FdTable[0].FilePos != 16384 {
		t.Errorf("File offset should be 16384, got %d", eng.FdTable[0].FilePos)
	}
	fmt.Printf("文件偏移量: %d (匹配请求的 16384 字节)\n", eng.FdTable[0].FilePos)
}

func TestSubStepFlow_FAULT_EFAULT(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0xFFFFFFFF,
		UseDoubleBuffer: false,
	}
	userCtx := &pb.UserContext{Uid: 1000, Gid: 1000, Username: "user1", HomeDir: "/home/user1"}
	eng := NewEngine(config, userCtx)

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}

	fmt.Printf("EFAULT 测试: %d 步终止, errCode=%s (expected EFAULT)\n", step, eng.Snapshot.FinalErrorCode)
	if step != 3 {
		t.Errorf("EFAULT should terminate at L0 sub-step 3 (access_ok), got step %d", step)
	}
	if eng.Snapshot.FinalErrorCode != "EFAULT (Bad address)" {
		t.Errorf("Expected EFAULT, got %s", eng.Snapshot.FinalErrorCode)
	}
}

func TestSubStepFlow_FAULT_EACCES(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/etc/shadow",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	userCtx := &pb.UserContext{Uid: 1000, Gid: 1000, Username: "user1", HomeDir: "/home/user1"}
	eng := NewEngine(config, userCtx)
	eng.InjectedFault = pb.FaultType_FAULT_PERMISSION_DENIED

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}

	fmt.Printf("EACCES 测试: %d 步终止 (L0=4 + L1=2), errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	if step != 6 {
		t.Errorf("EACCES should terminate at step 6 (L0:4 + L1:2), got %d", step)
	}
}

func TestFdTable_Lookup(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	userCtx := &pb.UserContext{Uid: 1000, Gid: 1000, Username: "user1", HomeDir: "/home/user1"}
	eng := NewEngine(config, userCtx)

	// Verify fd table initialization
	if len(eng.FdTable) != 1 {
		t.Fatalf("Expected 1 fd table entry, got %d", len(eng.FdTable))
	}
	entry := eng.FdTable[0]
	if entry.Fd != 3 {
		t.Errorf("Expected fd=3, got %d", entry.Fd)
	}
	if entry.FilePath != "/home/user1/notes.txt" {
		t.Errorf("Expected path=/home/user1/notes.txt, got %s", entry.FilePath)
	}
	if entry.Flags != 0 {
		t.Errorf("Expected flags=O_RDONLY(0), got %d", entry.Flags)
	}
	fmt.Printf("fd 表测试通过: fd=%d, path=%s, inode=%s, filePos=%d\n",
		entry.Fd, entry.FilePath, entry.InodePtr, entry.FilePos)
}
