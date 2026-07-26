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

	"wordflow/syncserver"
)

func main() {
	addr := flag.String("addr", ":9274", "Server listen address")
	dbPath := flag.String("db", "", "Database file path (default: UserConfigDir/WordWise/sync.db)")
	clean := flag.Bool("clean", false, "Clean soft-deleted entries older than 30 days on startup")
	flag.Parse()

	// WeChat Mini Program credentials (from environment variables)
	appID := os.Getenv("WECHAT_APP_ID")
	appSecret := os.Getenv("WECHAT_APP_SECRET")

	// Default database path
	if *dbPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			configDir = "."
		}
		*dbPath = filepath.Join(configDir, "WordFlow", "sync.db")
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     WordFlow Sync Server v1.1.0          ║")
	fmt.Println("║     English Dictionary - Multi-device    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Database: %s\n", *dbPath)
	fmt.Printf("  Listen:   %s\n", *addr)
	if appID != "" {
		fmt.Printf("  WeChat:   AppID=%s (QR code auth enabled)\n", appID)
	} else {
		fmt.Printf("  WeChat:   (not configured, set WECHAT_APP_ID + WECHAT_APP_SECRET)\n")
	}
	fmt.Println()

	// Open store
	store, err := syncserver.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	// Clean old deleted entries if requested
	if *clean {
		deleted, err := store.CleanDeleted(30 * 24 * time.Hour)
		if err != nil {
			log.Printf("Cleanup failed: %v", err)
		} else if deleted > 0 {
			log.Printf("Cleaned %d expired deleted entries", deleted)
		}
	}

	// Start server
	srv := syncserver.NewServer(store, *addr, appID, appSecret)

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Server start failed: %v", err)
		}
	}()

	fmt.Println("Server started, press Ctrl+C to stop")
	fmt.Println()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println()
	fmt.Println("Shutting down server...")
	srv.Shutdown()
	fmt.Println("Server stopped")
}
