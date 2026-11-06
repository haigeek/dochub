package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dochub/internal/api"
	"dochub/internal/config"
	"dochub/internal/docs"
	"dochub/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	if err := db.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}
	if err := db.FailInterruptedJobs(ctx); err != nil {
		log.Printf("清理中断任务失败: %v", err)
	}
	if err := db.CleanupSessions(ctx); err != nil {
		log.Printf("清理过期会话失败: %v", err)
	}

	docsService, err := docs.NewService(db, cfg)
	if err != nil {
		log.Fatalf("初始化文档服务失败: %v", err)
	}
	docsService.Start(ctx, 2)

	handler := api.New(cfg, db, docsService).Router()
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("DocHub 已启动: %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()
	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Printf("服务关闭异常: %v", err)
	}
}
