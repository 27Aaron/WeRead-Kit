// wxread 启动本地 Web 服务,承载账号管理与自动阅读调度。
// 所有操作均通过浏览器界面完成,配置经环境变量注入。
package main

import (
    "context"
    "errors"
    "fmt"
    "net/http"
    "os"
    "os/signal"
    "time"

    "wxread/internal/store"
    "wxread/internal/web"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()
    dbPath := os.Getenv("WXREAD_DB")
    if dbPath == "" { dbPath = "data/wxread.db" }
    addr := os.Getenv("WXREAD_ADDR")
    if addr == "" { addr = "127.0.0.1:8080" }
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

