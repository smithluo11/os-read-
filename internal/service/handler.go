package service

import (
	"fmt"
	"io"
	"log"

	"io-simulator/api/pb"
	"io-simulator/internal/engine"
)

// IOSimulationService 实现了 proto 中定义的 IOSimulationEngineServer 接口
type IOSimulationService struct {
	pb.UnimplementedIOSimulationEngineServer
}

// StreamSimulation 核心双向流处理函数
func (s *IOSimulationService) StreamSimulation(stream pb.IOSimulationEngine_StreamSimulationServer) error {
	log.Println("⚡ 前端已连接，开启新的 I/O 模拟流...")
	var simEngine *engine.SimulationEngine

	for {
		// 1. 持续接收前端发来的控制指令
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("前端关闭了连接")
			return nil
		}
		if err != nil {
			log.Printf("接收前端指令错误: %v", err)
			return err
		}

		// 2. 根据前端指令更新状态机
		switch req.Action {
		case pb.SimControlCommand_ACTION_INIT:
			log.Printf("收到初始化请求: 读取文件 %s, 大小 %d 字节", req.Config.FilePath, req.Config.BytesToRead)

			// 提取用户上下文，若前端未传则默认普通用户 user1
			userCtx := req.UserContext
			if userCtx == nil {
				userCtx = &pb.UserContext{
					Uid:      1000,
					Gid:      1000,
					Username: "user1",
					HomeDir:  "/home/user1",
				}
			}
			log.Printf("用户上下文: uid=%d gid=%d username=%s home=%s",
				userCtx.Uid, userCtx.Gid, userCtx.Username, userCtx.HomeDir)

			// 跨 INIT 持久化页缓存：保存旧引擎的缓存数据
			var oldPageCache map[string][]byte
			if simEngine != nil && simEngine.PageCache != nil && len(simEngine.PageCache) > 0 {
				oldPageCache = simEngine.PageCache
				log.Printf("保留旧引擎的页缓存: %d 页", len(oldPageCache))
			}

			// 实例化新的状态机
			simEngine = engine.NewEngine(req.Config, userCtx)

			// 恢复页缓存：旧缓存存在且新配置也启用页缓存时继承
			if oldPageCache != nil && req.Config.UsePageCache {
				simEngine.PageCache = oldPageCache
				log.Printf("页缓存已继承到新引擎: %d 页", len(simEngine.PageCache))
			}

			// 把初始状态推给前端
			if err := stream.Send(simEngine.Snapshot); err != nil {
				return err
			}

		case pb.SimControlCommand_ACTION_STEP_NEXT:
			log.Println("收到单步执行请求...")
			if simEngine == nil {
				return fmt.Errorf("请先发送 ACTION_INIT 初始化系统")
			}

			// 已经完成则跳过，避免重复发送同一完成态
			if simEngine.Snapshot.IsFinished {
				log.Printf("模拟已结束，忽略多余的 STEP 请求")
				continue
			}

			// 推动状态机走一步
			snapshot, err := simEngine.NextStep()
			if err != nil {
				log.Printf("状态机执行完毕或出错: %v", err)
				// 发送最终快照 (首次完成)
				if sendErr := stream.Send(simEngine.Snapshot); sendErr != nil {
					log.Printf("发送最终快照失败: %v", sendErr)
					return sendErr
				}
				continue
			}

			// 将最新状态推送给前端
			if err := stream.Send(snapshot); err != nil {
				return err
			}

		case pb.SimControlCommand_ACTION_INJECT_FAULT:
			log.Printf("收到故障注入请求: %v", req.InjectedFault)
			if simEngine != nil {
				// 👉 直接赋给引擎的 InjectedFault 字段
				simEngine.InjectedFault = req.InjectedFault
			}
		}
	}
}
