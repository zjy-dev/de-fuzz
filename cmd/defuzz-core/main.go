// Command defuzz-core is the deterministic Go core for the agentic loop.
//
// It exposes two faces over one process, linking the same internal/ packages:
//   - gRPC (--grpc-addr): deterministic nodes called by the Python orchestrator
//     (build / coverage / oracle / checker-metadata). Registered in T010.
//   - MCP  (--mcp-addr):  read-only tools called by agents during ReAct.
//     Tools are registered in T016/T033/T036.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	_ "github.com/zjy-dev/de-fuzz/internal/oracle" // register oracle plugins
	"github.com/zjy-dev/de-fuzz/internal/service"
	pb "github.com/zjy-dev/de-fuzz/internal/service/pb"
)

func main() {
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:50051", "address for the deterministic gRPC server")
	mcpAddr := flag.String("mcp-addr", "127.0.0.1:50052", "address for the agent-facing MCP server")
	mechanism := flag.String("mechanism", "canary", "the single defense mechanism this run targets (canary|ibt|fortify)")
	toolchainsPath := flag.String("toolchains", "configs/toolchains.yaml", "path to the ISA→toolchain config")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	toolchains, err := service.LoadToolchains(*toolchainsPath)
	if err != nil {
		log.Printf("warning: toolchains config %s not loaded (%v); ISAs will yield error cells", *toolchainsPath, err)
		toolchains = &service.Toolchains{}
	}

	grpcServer := grpc.NewServer()
	pb.RegisterCheckerMetadataServiceServer(grpcServer, &service.CheckerMetadataServer{})
	pb.RegisterBuildServiceServer(grpcServer, service.NewBuildServer(toolchains))
	pb.RegisterOracleServiceServer(grpcServer, service.NewOracleServer(*mechanism, nil, toolchains))
	pb.RegisterCoverageServiceServer(grpcServer, service.NewCoverageServer(nil))

	mcpServer := &http.Server{Addr: *mcpAddr}
	// MCP tools (search_source/query_invariants/...) registered in T016+.

	grpcLn, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("grpc listen %s: %v", *grpcAddr, err)
	}

	go func() {
		log.Printf("gRPC server listening on %s", *grpcAddr)
		if err := grpcServer.Serve(grpcLn); err != nil {
			log.Printf("grpc server stopped: %v", err)
		}
	}()

	go func() {
		log.Printf("MCP server listening on %s", *mcpAddr)
		if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp server stopped: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	grpcServer.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mcpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("mcp shutdown: %v", err)
		os.Exit(1)
	}
}
