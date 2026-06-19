package main

import (
	"log"
	"net/http"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"google.golang.org/grpc"

	"io-simulator/api/pb"
	"io-simulator/internal/service"
)

func main() {
	// 1. 初始化原生的 gRPC Server
	grpcServer := grpc.NewServer()

	// 2. 将我们写的 handler 注册到 gRPC Server 上
	simService := &service.IOSimulationService{}
	pb.RegisterIOSimulationEngineServer(grpcServer, simService)

	// 3. 核心魔法：用 grpc-web 将原生 gRPC 包装起来
	// 这里配置允许跨域 (CORS)，因为前端在开发阶段通常是通过 localhost:3000 等不同端口访问的
	wrappedGrpc := grpcweb.WrapServer(grpcServer,
		grpcweb.WithOriginFunc(func(origin string) bool { return true }), // 允许所有跨域请求
		grpcweb.WithAllowedRequestHeaders([]string{"*"}),                 // 允许所有请求头
	)

	// 4. 创建一个标准 HTTP 处理器，将流量分发给 grpc-web
	handler := func(res http.ResponseWriter, req *http.Request) {
		// 如果是前端 gRPC-Web 发来的请求，交给 wrappedGrpc 处理
		if wrappedGrpc.IsGrpcWebRequest(req) || wrappedGrpc.IsAcceptableGrpcCorsRequest(req) {
			wrappedGrpc.ServeHTTP(res, req)
			return
		}
		// 其他普通 HTTP 请求（你甚至以后可以在这里托管编译好的前端静态文件）
		http.DefaultServeMux.ServeHTTP(res, req)
	}

	// 5. 启动 HTTP 服务，监听 8080 端口
	httpServer := &http.Server{
		Addr:    ":18083",
		Handler: http.HandlerFunc(handler),
	}

	log.Println("🚀 I/O 模拟器后端已启动！")
	log.Println("✅ 正在监听端口 :18083 (支持 gRPC-Web 与 CORS 跨域)")
	
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}