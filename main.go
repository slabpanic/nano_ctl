// @title nano_ctl REST API
// @version 1.0
// @description REST API for monitoring and controlling Leadshine motion cards.
// @BasePath /api/v1
// @schemes http
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "nano_ctl/pkg/proto/godmc/v1"
	"nano_ctl/routers/grpcapi"
	"nano_ctl/routers/httpapi"
	"nano_ctl/service"
	"nano_ctl/service/hardware"
	"nano_ctl/service/store"

	"google.golang.org/grpc"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(sigCtx); err != nil {
		log.Printf("nano_ctl stopped: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	db, err := store.Open(env("NANO_CTL_DB", "./data/nano_ctl.db"))
	if err != nil {
		return fmt.Errorf("open application database: %w", err)
	}
	defer db.Close()

	hw := hardware.NewController()
	return service.WithHardware(ctx, hw, func(ctx context.Context) error { return serve(ctx, hw, db) })
}

func serve(ctx context.Context, hw *hardware.Controller, db *store.Store) error {
	requestsCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	httpServer := &http.Server{
		Addr:              env("NANO_CTL_HTTP_ADDR", ":5000"),
		Handler:           httpapi.New(hw, db).Handler(),
		BaseContext:       func(net.Listener) context.Context { return requestsCtx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	httpListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen for HTTP: %w", err)
	}
	defer httpListener.Close()
	grpcListener, err := net.Listen("tcp", env("NANO_CTL_GRPC_ADDR", ":50051"))
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	defer grpcListener.Close()
	grpcServer := grpc.NewServer(grpc.WaitForHandlers(true), grpc.MaxConcurrentStreams(2048), grpc.MaxRecvMsgSize(4<<20), grpc.MaxSendMsgSize(4<<20))
	pb.RegisterMotionServiceServer(grpcServer, grpcapi.New(hw, db))

	errCh := make(chan error, 2)
	go func() { errCh <- httpServer.Serve(httpListener) }()
	go func() { errCh <- grpcServer.Serve(grpcListener) }()
	log.Printf("nano_ctl listening: REST %s, gRPC %s; hardware session opened on startup", httpListener.Addr(), grpcListener.Addr())

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, grpc.ErrServerStopped) {
			serveErr = fmt.Errorf("serve requests: %w", err)
		}
	}
	// Cancel HTTP/SSE request contexts before draining. Stop both protocols before
	// WithHardware closes the SDK; existing native calls must finish first.
	cancelRequests()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	httpDone := make(chan error, 1)
	go func() { httpDone <- httpServer.Shutdown(shutdownCtx) }()
	grpcDone := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(grpcDone) }()
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
		<-grpcDone
	}
	if err := <-httpDone; err != nil {
		_ = httpServer.Close()
		if !errors.Is(err, context.DeadlineExceeded) {
			serveErr = errors.Join(serveErr, err)
		}
	}
	return serveErr
}
