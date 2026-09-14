// wxread 启动本地 Web 服务,承载账号管理与挑战赛调度。
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

    "wxread/internal/store"
    "wxread/internal/web"
)

func main() {
    loadDotEnv(".env")
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()
    dbPath := os.Getenv("WXREAD_DB")
    if dbPath == "" { dbPath = "data/wxread.db" }
    host := os.Getenv("WXREAD_HOST"); if host == "" { host = "127.0.0.1" }
    port := os.Getenv("WXREAD_PORT"); if port == "" { port = "8080" }
    addr := host + ":" + port
    db, err := store.Open(dbPath)
    if err != nil { panic(fmt.Sprintf("打开数据库失败: %v", err)) }
    defer db.Close()
    app := web.New(db)
    app.StartFarmScheduler(ctx)
    app.ResumeInterrupted()
    store.AddLog(db, "info", "serve", "", "Web 服务已启动,监听 "+addr)
    srv := &http.Server{Addr: addr, Handler: app.Handler()}
    go func() {
        <-ctx.Done()
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        _ = srv.Shutdown(shutdownCtx)
    }()
    fmt.Printf("wxread Web UI: http://%s\n", addr)
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        fmt.Fprintf(os.Stderr, "Web 服务失败: %v\n", err)
    }
}

// loadDotEnv loads simple KEY=VALUE entries without overriding real environment variables.
func loadDotEnv(path string) {
    b, err := os.ReadFile(path); if err != nil { return }
    for _, line := range strings.Split(string(b), "\n") {
        line = strings.TrimSpace(line); if line == "" || strings.HasPrefix(line, "#") { continue }
        k, v, ok := strings.Cut(line, "="); if !ok { continue }
        k = strings.TrimSpace(k); v = strings.Trim(strings.TrimSpace(v), "\"'")
        if k != "" { if _, exists := os.LookupEnv(k); !exists { _ = os.Setenv(k, v) } }
    }
}
