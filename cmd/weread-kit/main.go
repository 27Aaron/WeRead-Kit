// weread-kit 启动本地 Web 服务,承载账号管理与阅读挑战调度。
// 所有操作均通过浏览器界面完成,配置经环境变量注入。
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/web"
)

func main() {
	loadDotEnv(".env")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dbPath := os.Getenv("WEREAD_DB")
	if dbPath == "" {
		dbPath = "data/weread.db"
	}
	host := os.Getenv("WEREAD_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("WEREAD_PORT")
	if port == "" {
		port = "8080"
	}
	authEnabled := os.Getenv("WEREAD_USERNAME") != ""
	if authEnabled != (os.Getenv("WEREAD_PASSWORD") != "") {
		fatalf("WEREAD_USERNAME 和 WEREAD_PASSWORD 必须同时设置或同时留空")
	}
	addr := net.JoinHostPort(host, port)
	db, err := store.Open(dbPath)
	if err != nil {
		fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	app := web.New(db)
	defer app.Close()
	app.StartFarmScheduler(ctx)
	app.StartWeeklyClaimScheduler(ctx)
	app.ResumeInterrupted()
	store.AddLog(db, "info", "serve", "", "Web 服务已启动,监听 "+addr)
	if !authEnabled && !isLoopback(host) {
		// 未設密碼且監聽非回環位址:能存取連接埠的人都能讀取帳號 Token,必須明確提醒。
		msg := "未设置 WEREAD_USERNAME/WEREAD_PASSWORD,且监听地址 " + host + " 不是本机回环,能访问该端口的任何人都可管理账号并读取凭据"
		store.AddLog(db, "warn", "serve", "", msg)
		fmt.Fprintln(os.Stderr, "警告: "+msg)
	}
	srv := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	fmt.Printf("weread-kit Web UI: http://%s\n", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "Web 服务失败: %v\n", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loadDotEnv loads simple KEY=VALUE entries without overriding real environment variables.
func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), "\"'")
		if k != "" {
			if _, exists := os.LookupEnv(k); !exists {
				_ = os.Setenv(k, v)
			}
		}
	}
}
