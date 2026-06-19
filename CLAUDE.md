# I/O Simulator 后端项目规则

## Git 提交规则
- 每次完成代码优化/功能实现后，必须执行 git commit 提交到本地仓库
- Commit message 使用中文，格式：`feat/fix/docs/refactor: 简短描述`
- 不主动 git push，除非用户明确要求

## Proto 修改规则
- 修改 `api/proto/*.proto` 后必须运行 `make proto` 重新生成 pb 代码
- 禁止手动编辑 `api/pb/` 目录下的 `.go` 文件（均由 protoc 自动生成）

## 项目结构
- `api/proto/` — Protobuf 定义
- `api/pb/` — 生成的 Go 代码（禁止手动修改）
- `cmd/server/` — 服务入口
- `internal/engine/` — 核心状态机引擎
- `internal/service/` — gRPC 服务处理层
- `web/` — 前端静态资源及配合文档
