// grpc-demo 是 gRPC 调试的本地演示服务器（明文 :50051）：复用 gRPC 官方的
// interop 测试服务，一元 / 服务端流式 / 客户端流式 / 双向流式都有，且开了
// 服务器反射，「获取方法」能直接列出全部方法。
//
// 运行：go run ./scripts/grpc-demo
// 常用试法（target 填 127.0.0.1:50051，传输选明文）：
//   - 一元：grpc.testing.TestService/UnaryCall，消息 {"responseSize": 16}
//   - 服务端流式：StreamingOutputCall，{"responseParameters":[{"size":8},{"size":8},{"size":8}]}
//   - 客户端流式：StreamingInputCall，逐条 {"payload":{"body":"aGk="}}，「结束发送」后收聚合
//   - 双向：FullDuplexCall，逐条带 responseParameters 的消息
package main

import (
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/interop"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
)

func main() {
	lis, err := net.Listen("tcp", "127.0.0.1:50051")
	if err != nil {
		log.Fatal(err)
	}
	srv := grpc.NewServer()
	testgrpc.RegisterTestServiceServer(srv, interop.NewTestServer())
	reflection.Register(srv)
	log.Println("grpc demo server listening on 127.0.0.1:50051 (plaintext, reflection on)")
	log.Fatal(srv.Serve(lis))
}
