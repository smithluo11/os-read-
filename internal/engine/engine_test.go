package engine

import (
	"fmt"
	"io-simulator/api/pb"
	"testing"
)

// =========================================================================
// Helpers
// =========================================================================

// runUntilFinished steps through the simulation and returns total steps + final snapshot.
func runUntilFinished(t *testing.T, eng *SimulationEngine) (int, *pb.SystemSnapshot) {
	t.Helper()
	step := 0
	for !eng.Snapshot.IsFinished && step < 200 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}
	return step, eng.Snapshot
}

// defaultConfig returns a standard single-buffer config.
func defaultConfig() *pb.ReadRequestConfig {
	return &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    16384,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
}

func user1Ctx() *pb.UserContext {
	return &pb.UserContext{Uid: 1000, Gid: 1000, Username: "user1", HomeDir: "/home/user1"}
}

func rootCtx() *pb.UserContext {
	return &pb.UserContext{Uid: 0, Gid: 0, Username: "root", HomeDir: "/root"}
}

// =========================================================================
// Normal Flows
// =========================================================================

func TestSubStepFlow_SingleBuffer(t *testing.T) {
	config := defaultConfig()
	userCtx := user1Ctx()
	eng := NewEngine(config, userCtx)

	layerNames := map[int32]string{0: "USER", 1: "VFS", 2: "DRV", 3: "INT", 4: "HW"}

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

		if snap.SubStep < 1 || snap.SubStep > snap.TotalSubSteps {
			t.Errorf("Step %d: snap.SubStep=%d out of range [1,%d]",
				step, snap.SubStep, snap.TotalSubSteps)
		}
	}

	fmt.Printf("单缓冲测试: %d 步完成, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	fmt.Printf("各层子步骤数: USER=%d, VFS=%d, DRV=%d, HW=%d, INT=%d\n",
		layerStepCounts["USER"], layerStepCounts["VFS"], layerStepCounts["DRV"],
		layerStepCounts["HW"], layerStepCounts["INT"])

	if layerStepCounts["USER"] != 4 {
		t.Errorf("USER layer: expected 4 sub-steps, got %d", layerStepCounts["USER"])
	}
	if layerStepCounts["VFS"] != 5 {
		t.Errorf("VFS layer: expected 5 sub-steps, got %d", layerStepCounts["VFS"])
	}
	if layerStepCounts["DRV"] != 3 {
		t.Errorf("DRV layer: expected 3 sub-steps, got %d", layerStepCounts["DRV"])
	}

	if eng.FdTable[0].FilePos != 16384 {
		t.Errorf("File offset should be 16384, got %d", eng.FdTable[0].FilePos)
	}
	fmt.Printf("文件偏移量: %d (匹配请求的 16384 字节)\n", eng.FdTable[0].FilePos)
}

func TestDoubleBuffer_PingPong(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    16384, // 4 个 4KB chunk
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: true,
	}
	userCtx := user1Ctx()
	eng := NewEngine(config, userCtx)

	// Verify TotalChunks
	if eng.TotalChunks != 4 {
		t.Fatalf("Expected 4 chunks (16384/4096), got %d", eng.TotalChunks)
	}

	step, snap := runUntilFinished(t, eng)

	fmt.Printf("双缓冲测试: %d 步完成, errCode=%s\n", step, snap.FinalErrorCode)

	// Verify user buffer data was accumulated across chunks
	if len(snap.MemoryState.UserBufferData) != 16384 {
		t.Errorf("Expected 16384 bytes in user buffer, got %d", len(snap.MemoryState.UserBufferData))
	}

	// Verify ping-pong: buffers should have swapped at least once
	if snap.MemoryState.CurrentChunk < 2 {
		t.Errorf("Expected at least 2 chunks processed, CurrentChunk=%d", snap.MemoryState.CurrentChunk)
	}

	// Verify file offset
	if eng.FdTable[0].FilePos != 16384 {
		t.Errorf("File offset should be 16384, got %d", eng.FdTable[0].FilePos)
	}
}

func TestDoubleBuffer_SmallRead(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    1024, // <1 chunk
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: true,
	}
	eng := NewEngine(config, user1Ctx())

	if eng.TotalChunks != 1 {
		t.Errorf("Expected 1 chunk for 1024B, got %d", eng.TotalChunks)
	}

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("双缓冲小文件测试: %d 步完成, %d 字节\n", step, len(snap.MemoryState.UserBufferData))
}

// =========================================================================
// Terminal Faults
// =========================================================================

func TestSubStepFlow_FAULT_EFAULT(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0xFFFFFFFF,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

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
	eng := NewEngine(config, user1Ctx())
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
	if eng.Snapshot.FinalErrorCode != "EACCES (Permission denied)" {
		t.Errorf("Expected EACCES, got %s", eng.Snapshot.FinalErrorCode)
	}
}

func TestFAULT_EPERM_PathTraversal(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "../../etc/shadow",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())
	eng.InjectedFault = pb.FaultType_FAULT_PATH_TRAVERSAL

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}

	fmt.Printf("EPERM 测试: %d 步终止, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	if step != 6 {
		t.Errorf("EPERM should terminate at step 6 (L0:4 + L1:2), got %d", step)
	}
}

func TestFAULT_ENOENT_FileNotFound(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/nonexistent",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())
	eng.InjectedFault = pb.FaultType_FAULT_FILE_NOT_FOUND

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}

	fmt.Printf("ENOENT 测试: %d 步终止, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	if step != 6 {
		t.Errorf("ENOENT should terminate at step 6 (L0:4 + L1:2), got %d", step)
	}
}

func TestFAULT_EIO_HardwareTimeout(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())
	eng.InjectedFault = pb.FaultType_FAULT_HARDWARE_TIMEOUT

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		_, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++
	}

	fmt.Printf("EIO 测试: %d 步终止, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)
	if eng.Snapshot.FinalErrorCode != "EIO (Input/output error)" {
		t.Errorf("Expected EIO, got %s", eng.Snapshot.FinalErrorCode)
	}
	// Should terminate at L4 sub-step 2
	if eng.Snapshot.HardwareState.StatusRegister != "0x03: DEVICE_ERROR" {
		t.Errorf("Expected DEVICE_ERROR, got %s", eng.Snapshot.HardwareState.StatusRegister)
	}
	if eng.Snapshot.HardwareState.DmaStatus != "ERROR" {
		t.Errorf("Expected DMA ERROR, got %s", eng.Snapshot.HardwareState.DmaStatus)
	}
}

// =========================================================================
// EAGAIN — Retryable Error
// =========================================================================

func TestEAGAIN_RetryThenSucceed(t *testing.T) {
	config := defaultConfig()
	eng := NewEngine(config, user1Ctx())
	eng.InjectedFault = pb.FaultType_FAULT_EAGAIN

	step := 0
	retrySeen := 0

	for !eng.Snapshot.IsFinished && step < 50 {
		snap, err := eng.NextStep()
		if err != nil {
			t.Fatalf("Step %d error: %v", step, err)
		}
		step++

		// Count EAGAIN retries
		if snap.MemoryState.RetryCount > 0 && snap.CurrentActiveLayer == pb.SystemSnapshot_LAYER_DRIVER {
			retrySeen = int(snap.MemoryState.RetryCount)
		}
	}

	fmt.Printf("EAGAIN 测试: %d 步完成, errCode=%s, retries=%d\n",
		step, eng.Snapshot.FinalErrorCode, retrySeen)

	if retrySeen != 3 {
		t.Errorf("Expected 3 retries, got %d", retrySeen)
	}
	if eng.Snapshot.FinalErrorCode != "SUCCESS" {
		t.Errorf("EAGAIN should succeed after retries, got %s", eng.Snapshot.FinalErrorCode)
	}
	// Normal flow is 18 steps + 3 extra retry steps = ~21 steps
	if step < 20 || step > 25 {
		t.Errorf("Unexpected step count %d (expected ~21)", step)
	}
}

// =========================================================================
// Page Cache
// =========================================================================

func TestPageCache_Hit(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
		UsePageCache:    true,
	}
	eng := NewEngine(config, user1Ctx())

	// First read: cache miss → full I/O
	step1, snap1 := runUntilFinished(t, eng)
	fmt.Printf("首次读取 (miss): %d 步, %d 字节, cacheHit=%v\n",
		step1, len(snap1.MemoryState.UserBufferData), snap1.MemoryState.CacheHit)

	if snap1.MemoryState.CacheHit {
		t.Error("First read should be a cache miss")
	}
	if snap1.FinalErrorCode != "SUCCESS" {
		t.Errorf("First read should succeed, got %s", snap1.FinalErrorCode)
	}

	// Second read: same file → cache hit
	eng2 := NewEngine(config, user1Ctx())
	eng2.PageCache = eng.PageCache // share the cache
	eng2.Snapshot.MemoryState.CachedPages = uint32(len(eng2.PageCache))

	step2, snap2 := runUntilFinished(t, eng2)
	fmt.Printf("二次读取 (hit): %d 步, %d 字节, cacheHit=%v\n",
		step2, len(snap2.MemoryState.UserBufferData), snap2.MemoryState.CacheHit)

	if !snap2.MemoryState.CacheHit {
		t.Error("Second read should be a cache hit")
	}
	// Cache hit skips DRV→HW→INT: should be much faster
	if step2 >= step1 {
		t.Errorf("Cache hit should be fewer steps than miss: %d >= %d", step2, step1)
	}
	if step2 != 7 {
		t.Errorf("Cache hit should be 7 steps (L0:4 + L1:3, terminates at cache-hit sub-step), got %d", step2)
	}
}

func TestPageCache_Disabled(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    4096,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
		UsePageCache:    false,
	}
	eng := NewEngine(config, user1Ctx())

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("缓存禁用: %d 步, cacheHit=%v, cachedPages=%d\n",
		step, snap.MemoryState.CacheHit, snap.MemoryState.CachedPages)

	if snap.MemoryState.CacheHit {
		t.Error("Cache disabled should not report hit")
	}
}

// =========================================================================
// Filesystem Permission Checks
// =========================================================================

func TestRootBypassSensitiveFile(t *testing.T) {
	// Root can read /etc/shadow despite it being sensitive
	config := &pb.ReadRequestConfig{
		FilePath:       "/etc/shadow",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, rootCtx())

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("Root 读敏感文件: %d 步, errCode=%s\n", step, snap.FinalErrorCode)

	if snap.FinalErrorCode != "SUCCESS" {
		t.Errorf("Root should be able to read /etc/shadow, got %s", snap.FinalErrorCode)
	}
}

func TestUserCannotReadSensitiveFile(t *testing.T) {
	// Normal user cannot read /etc/shadow
	config := &pb.ReadRequestConfig{
		FilePath:       "/etc/shadow",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		eng.NextStep()
		step++
	}

	fmt.Printf("普通用户读敏感文件: %d 步, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)

	if eng.Snapshot.FinalErrorCode == "SUCCESS" {
		t.Error("Normal user should NOT be able to read /etc/shadow")
	}
}

func TestUserCanReadOtherReadable(t *testing.T) {
	// /tmp/data.bin has 0666 permissions → other can read
	config := &pb.ReadRequestConfig{
		FilePath:       "/tmp/data.bin",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	// uid=2000, gid=2000 — not the owner, not in the group
	otherCtx := &pb.UserContext{Uid: 2000, Gid: 2000, Username: "other", HomeDir: "/home/other"}
	eng := NewEngine(config, otherCtx)

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("Other 读 0666 文件: %d 步, errCode=%s\n", step, snap.FinalErrorCode)

	if snap.FinalErrorCode != "SUCCESS" {
		t.Errorf("Other user should read /tmp/data.bin (0666), got %s", snap.FinalErrorCode)
	}
}

func TestGroupReadPermission(t *testing.T) {
	// /home/user1/notes.txt: uid=1000, gid=1000, perm=0644
	// User with uid=2000, gid=1000 (same group) should be able to read (group r-- bit)
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	sameGroupCtx := &pb.UserContext{Uid: 2000, Gid: 1000, Username: "colleague", HomeDir: "/home/colleague"}
	eng := NewEngine(config, sameGroupCtx)

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("同组用户读 owner 文件: %d 步, errCode=%s\n", step, snap.FinalErrorCode)

	if snap.FinalErrorCode != "SUCCESS" {
		t.Errorf("Same-group user should read file (g+r), got %s", snap.FinalErrorCode)
	}
}

func TestNoReadPermission(t *testing.T) {
	// /root/secret.key: uid=0, gid=0, perm=0400
	// Non-root, non-owner cannot read
	config := &pb.ReadRequestConfig{
		FilePath:       "/root/secret.key",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		eng.NextStep()
		step++
	}

	fmt.Printf("无权限用户读 secret.key: %d 步, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)

	if eng.Snapshot.FinalErrorCode == "SUCCESS" {
		t.Error("user1 should NOT be able to read /root/secret.key")
	}
}

func TestPathTraversalDetection(t *testing.T) {
	// Real path traversal (without InjectedFault) should also be caught
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/../../etc/shadow",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		eng.NextStep()
		step++
	}

	fmt.Printf("真实路径穿越: %d 步, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)

	if eng.Snapshot.FinalErrorCode == "SUCCESS" {
		t.Error("Path traversal should be detected and blocked")
	}
}

func TestFileNotFound_Real(t *testing.T) {
	// Real file-not-found (without InjectedFault) via filesystem validation
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/ghost.txt",
		BytesToRead:    1024,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

	step := 0
	for !eng.Snapshot.IsFinished && step < 20 {
		eng.NextStep()
		step++
	}

	fmt.Printf("真实文件不存在: %d 步, errCode=%s\n", step, eng.Snapshot.FinalErrorCode)

	if eng.Snapshot.FinalErrorCode == "SUCCESS" {
		t.Error("Non-existent file should return ENOENT")
	}
}

// =========================================================================
// FD Table
// =========================================================================

func TestFdTable_Lookup(t *testing.T) {
	config := defaultConfig()
	eng := NewEngine(config, user1Ctx())

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

// =========================================================================
// DMA Registers
// =========================================================================

func TestDMA_Registers(t *testing.T) {
	config := defaultConfig()
	eng := NewEngine(config, user1Ctx())

	// Step to hardware layer
	step := 0
	var hwSnap *pb.SystemSnapshot
	for !eng.Snapshot.IsFinished && step < 50 {
		snap, _ := eng.NextStep()
		step++
		if snap.CurrentActiveLayer == pb.SystemSnapshot_LAYER_HARDWARE {
			hwSnap = snap
		}
	}

	if hwSnap == nil {
		t.Fatal("Never reached hardware layer")
	}

	hw := hwSnap.HardwareState
	if hw.DmaSource == "" || hw.DmaSource == "—" {
		t.Errorf("DMA source should be set, got '%s'", hw.DmaSource)
	}
	if hw.DmaDestination == "" || hw.DmaDestination == "—" {
		t.Errorf("DMA destination should be set, got '%s'", hw.DmaDestination)
	}
	if hw.DmaCount == 0 {
		t.Errorf("DMA count should be >0, got %d", hw.DmaCount)
	}
	fmt.Printf("DMA 寄存器: src=%s dst=%s count=%d status=%s\n",
		hw.DmaSource, hw.DmaDestination, hw.DmaCount, hw.DmaStatus)
}

// =========================================================================
// Edge Cases
// =========================================================================

func TestEdgeCase_ZeroRead(t *testing.T) {
	config := &pb.ReadRequestConfig{
		FilePath:       "/home/user1/notes.txt",
		BytesToRead:    0,
		UserBufferAddr: 0x10000000,
		UseDoubleBuffer: false,
	}
	eng := NewEngine(config, user1Ctx())

	step, snap := runUntilFinished(t, eng)
	fmt.Printf("零字节读取: %d 步, errCode=%s, dataLen=%d\n",
		step, snap.FinalErrorCode, len(snap.MemoryState.UserBufferData))

	if snap.FinalErrorCode != "SUCCESS" {
		t.Errorf("Zero-byte read should succeed, got %s", snap.FinalErrorCode)
	}
}

func TestNextStep_AfterFinish(t *testing.T) {
	config := defaultConfig()
	eng := NewEngine(config, user1Ctx())

	// Run to completion
	runUntilFinished(t, eng)

	// Calling NextStep after finish should return an error
	_, err := eng.NextStep()
	if err == nil {
		t.Error("NextStep() after finish should return an error")
	}
}
