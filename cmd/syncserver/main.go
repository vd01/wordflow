package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wordwise/syncserver"
)

func main() {
	addr := flag.String("addr", ":9274", "服务器监听地址")
	dbPath := flag.String("db", "", "数据库文件路径 (默认: 用户配置目录/WordWise/sync.db)")
	clean := flag.Bool("clean", false, "启动时清理30天前的已删除记录")
	flag.Parse()

	// Default database path
	if *dbPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			configDir = "."
		}
		*dbPath = filepath.Join(configDir, "WordWise", "sync.db")
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     WordWise Sync Server v1.0.0          ║")
	fmt.Println("║     英语词典助手 - 多设备同步服务         ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  数据库: %s\n", *dbPath)
	fmt.Printf("  监听地址: %s\n", *addr)
	fmt.Println()

	// Open store
	store, err := syncserver.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("❌ 打开数据库失败: %v", err)
	}
	defer store.Close()

	// Clean old deleted entries if requested
	if *clean {
		deleted, err := store.CleanDeleted(30 * 24 * time.Hour)
		if err != nil {
			log.Printf("⚠️ 清理失败: %v", err)
		} else if deleted > 0 {
			log.Printf("🧹 已清理 %d 条过期删除记录", deleted)
		}
	}

	// Start server
	srv := syncserver.NewServer(store, *addr)

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("❌ 服务器启动失败: %v", err)
		}
	}()

	fmt.Println("✅ 服务器已启动，按 Ctrl+C 停止")
	fmt.Println()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println()
	fmt.Println("🛑 正在关闭服务器...")
	srv.Shutdown()
	fmt.Println("👋 服务器已停止")
}
