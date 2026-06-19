package engine

import (
	"fmt"
	"strings"

	"io-simulator/api/pb"
)

// FileMetadata 模拟文件系统中的文件元数据
type FileMetadata struct {
	OwnerUID    uint32
	GroupGID    uint32
	Permissions uint32 // Unix 风格权限位，如 0644, 0600
	IsSensitive bool   // 敏感文件仅 root (UID=0) 可读
}

// FileSystemDB 模拟的文件系统目录
var FileSystemDB = map[string]*FileMetadata{
	"/etc/shadow":            {OwnerUID: 0, GroupGID: 0, Permissions: 0600, IsSensitive: true},
	"/sys/kernel/secure":     {OwnerUID: 0, GroupGID: 0, Permissions: 0400, IsSensitive: true},
	"/root/secret.key":       {OwnerUID: 0, GroupGID: 0, Permissions: 0400, IsSensitive: true},
	"/home/user1/notes.txt":  {OwnerUID: 1000, GroupGID: 1000, Permissions: 0644, IsSensitive: false},
	"/home/user1/public.txt": {OwnerUID: 1000, GroupGID: 1000, Permissions: 0644, IsSensitive: false},
	"/tmp/data.bin":          {OwnerUID: 1000, GroupGID: 1000, Permissions: 0666, IsSensitive: false},
}

// hasTraversalPattern 检测路径中是否包含 ".." 路径穿越模式
func hasTraversalPattern(path string) bool {
	return strings.Contains(path, "../") || strings.Contains(path, `..\`)
}

// resolvePath 规范化路径，折叠 "." 和 ".." 段，返回规范绝对路径
func resolvePath(path string) string {
	parts := strings.Split(path, "/")
	var resolved []string
	for _, p := range parts {
		switch p {
		case "", ".":
			continue
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		default:
			resolved = append(resolved, p)
		}
	}
	return "/" + strings.Join(resolved, "/")
}

// escapesHomeDir 检查解析后的路径是否逃逸出用户的主目录范围
func escapesHomeDir(resolvedPath, homeDir string) bool {
	if resolvedPath == homeDir {
		return false
	}
	return !strings.HasPrefix(resolvedPath, homeDir+"/")
}

// checkReadPermission 根据 Unix 权限位检查用户是否有读权限
func checkReadPermission(file *FileMetadata, uid, gid uint32) bool {
	if uid == 0 {
		return true // root 绕过所有权限检查
	}
	if uid == file.OwnerUID {
		return (file.Permissions>>6)&4 != 0 // owner read bit
	}
	if gid == file.GroupGID {
		return (file.Permissions>>3)&4 != 0 // group read bit
	}
	return file.Permissions&4 != 0 // other read bit
}

// ValidationError 文件访问校验失败的错误
type ValidationError struct {
	Code        string // "ENOENT", "EACCES", "EFAULT"
	Description string // 人类可读的描述，用于前端快照展示
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Description)
}

// ValidateFileAccess 执行完整的文件访问校验管线：
// 1. 路径穿越检测 → 2. 文件存在检查 → 3. 敏感文件检查 → 4. Unix 权限位检查
func ValidateFileAccess(userCtx *pb.UserContext, rawPath string) error {
	// 1. 检测路径穿越模式
	if hasTraversalPattern(rawPath) {
		resolved := resolvePath(rawPath)
		if escapesHomeDir(resolved, userCtx.HomeDir) {
			return &ValidationError{
				Code: "EFAULT",
				Description: fmt.Sprintf(
					"路径遍历攻击被检测！路径 %q 解析后为 %q，逃逸了用户主目录 %q",
					rawPath, resolved, userCtx.HomeDir),
			}
		}
	}

	// 2. 解析路径并检查文件是否存在
	resolvedPath := resolvePath(rawPath)
	fileEntry, exists := FileSystemDB[resolvedPath]
	if !exists {
		return &ValidationError{
			Code:        "ENOENT",
			Description: fmt.Sprintf("文件 %q 不存在于文件系统数据库中", resolvedPath),
		}
	}

	// 3. 敏感文件检查（仅 root 可读）
	if fileEntry.IsSensitive && userCtx.Uid != 0 {
		return &ValidationError{
			Code: "EACCES",
			Description: fmt.Sprintf(
				"文件 %q 是敏感系统文件，仅 root 可读取。当前用户 UID=%d 权限不足",
				resolvedPath, userCtx.Uid),
		}
	}

	// 4. Unix 权限位检查
	if !checkReadPermission(fileEntry, userCtx.Uid, userCtx.Gid) {
		return &ValidationError{
			Code: "EACCES",
			Description: fmt.Sprintf(
				"文件 %q 的 Unix 权限 %04o 拒绝当前用户 (UID=%d, GID=%d) 读取",
				resolvedPath, fileEntry.Permissions, userCtx.Uid, userCtx.Gid),
		}
	}

	return nil
}
