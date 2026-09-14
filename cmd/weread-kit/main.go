// weread-kit 启动本地 Web 服务,承载账号管理与阅读挑战调度。
// 所有操作均通过浏览器界面完成,配置经环境变量注入。
package main

import (
	"context"
	"errors"
	"fmt"
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
	if (os.Getenv("WEREAD_USERNAME") == "") != (os.Getenv("WEREAD_PASSWORD") == "") {
		panic("WEREAD_USERNAME 和 WEREAD_PASSWORD 必须同时设置或同时留空")
	}
	addr := host + ":" + port
	db, err := store.Open(dbPath)
	if err != nil {
		panic(fmt.Sprintf("打开数据库失败: %v", err))
	}
	defer db.Close()
	app := web.New(db)
	defer app.Close()
	app.StartFarmScheduler(ctx)
	app.StartWeeklyClaimScheduler(ctx)
	app.ResumeInterrupted()
	store.AddLog(db, "info", "serve", "", "Web 服务已启动,监听 "+addr)
	srv := &http.Server{Addr: addr, Handler: app.Handler()}
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
