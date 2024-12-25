// 命令 wxread:终端扫码登录微信读书,凭据存 SQLite,并自动续期保持长期可用。
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	qrterminal "github.com/mdp/qrterminal/v3"

	"wxread/internal/store"
	"wxread/internal/web"
	"wxread/internal/weread"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var code int
	switch os.Args[1] {
	case "login":
		code = cmdLogin(ctx, os.Args[2:])
	case "token":
		code = cmdToken(ctx, os.Args[2:])
	case "refresh":
		code = cmdRefresh(ctx, os.Args[2:])
	case "keepalive":
		code = cmdKeepalive(ctx, os.Args[2:])
	case "serve":
		code = cmdServe(ctx, os.Args[2:])
	case "show":
		code = cmdShow(ctx, os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Printf("未知命令 %q\n\n", os.Args[1])
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Print(`wxread — 微信读书扫码登录与凭据自动续期

用法:
  wxread login     [--alias 别名] [--db 路径]      终端扫码登录,凭据写入 SQLite
  wxread token     [--alias 别名] [--max-age 24h]  输出可用的 accessToken,过期自动刷新
  wxread refresh   [--alias 别名]                  立刻用 refreshToken 换新凭据
  wxread keepalive [--alias 别名] [--every 24h]    周期刷新,长期不用的账号也不会失效
  wxread serve     [--addr 127.0.0.1:8080]        启动 Web UI(扫码添加账号、备注、续期)
  wxread show      [--alias 别名]                  查看账号信息(不含令牌)

选项:
  --alias  账号别名,默认 default(可用环境变量 WXREAD_ACCOUNT 覆盖)
  --db     SQLite 路径,默认 ./data/wxread.db(可用 WXREAD_DB 覆盖)
`)
}

func commonFlags(fs *flag.FlagSet) (*string, *string) {
	alias := fs.String("alias", envOr("WXREAD_ACCOUNT", "default"), "账号别名")
	dbPath := fs.String("db", defaultDB(), "SQLite 数据库路径")
	return alias, dbPath
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDB() string {
	if v := os.Getenv("WXREAD_DB"); v != "" {
		return v
	}
	// 相对当前工作目录:在项目根目录运行时即 <项目根>/data/wxread.db。
	p, err := store.DefaultPath()
	if err != nil {
		return "wxread.db"
	}
	return p
}

func mustOpenDB(path string) *sql.DB {
	db, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开数据库失败: %v\n", err)
		os.Exit(1)
	}
	return db
}

func loadOrExit(db *sql.DB, alias string) *store.Credential {
	c, err := store.Load(db, alias)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "账号 %q 还未登录,请先运行: wxread login --alias %s\n", alias, alias)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取凭据失败: %v\n", err)
		os.Exit(1)
	}
	return c
}

func toWeread(c *store.Credential) *weread.Credentials {
	return &weread.Credentials{
		Vid:          c.Vid,
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		DeviceID:     c.DeviceID,
	}
}

func toStore(alias string, c *weread.Credentials) *store.Credential {
	return &store.Credential{
		Alias:        alias,
		Vid:          c.Vid,
		RefreshToken: c.RefreshToken,
		DeviceID:     c.DeviceID,
		AccessToken:  c.AccessToken,
	}
}

func cmdLogin(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()

	// 复用已有 deviceId,保持同一台"设备"身份,避免账号下堆积陌生设备。
	deviceID := ""
	if old, err := store.Load(db, *alias); err == nil {
		deviceID = old.DeviceID
	}

	creds, err := weread.NewClient().Login(ctx, deviceID,
		func(confirmURL string) {
			fmt.Println("请用微信扫描二维码,登录微信读书:")
			fmt.Println()
			qrterminal.GenerateWithConfig(confirmURL, qrterminal.Config{
				Level:          qrterminal.L,
				Writer:         os.Stdout,
				HalfBlocks:     true,
				BlackChar:      qrterminal.BLACK_BLACK,
				WhiteChar:      qrterminal.WHITE_WHITE,
				BlackWhiteChar: qrterminal.BLACK_WHITE,
				WhiteBlackChar: qrterminal.WHITE_BLACK,
				QuietZone:      1,
			})
			fmt.Println()
			fmt.Println("无法扫码时,在微信中打开下面的链接确认:")
			fmt.Println(confirmURL)
			fmt.Println()
			fmt.Println("等待扫码中,最长 5 分钟,Ctrl+C 取消……")
		},
		func(status string) { fmt.Println(status) },
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("已取消")
			return 130
		}
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		return 1
	}
	if err := store.Save(db, toStore(*alias, creds)); err != nil {
		fmt.Fprintf(os.Stderr, "保存凭据失败: %v\n", err)
		return 1
	}
	fmt.Printf("登录成功:alias=%s vid=%s deviceId=%s\n凭据已写入 %s\n", *alias, creds.Vid, creds.DeviceID, *dbPath)
	return 0
}

func cmdToken(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	maxAge := fs.Duration("max-age", 24*time.Hour, "accessToken 缓存超过该时长就先刷新再输出")
	force := fs.Bool("force", false, "无论缓存多新都强制刷新")
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)

	if *force || time.Since(c.RotatedAt) > *maxAge {
		refreshed, err := weread.NewClient().Refresh(ctx, toWeread(c))
		if err != nil {
			fmt.Fprintf(os.Stderr, "刷新失败: %v\n", err)
			return 1
		}
		if err := store.Save(db, toStore(*alias, refreshed)); err != nil {
			fmt.Fprintf(os.Stderr, "保存凭据失败: %v\n", err)
			return 1
		}
		c.AccessToken = refreshed.AccessToken
		fmt.Fprintln(os.Stderr, "accessToken 已刷新")
	}
	fmt.Println(c.AccessToken)
	return 0
}

func cmdRefresh(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)

	refreshed, err := weread.NewClient().Refresh(ctx, toWeread(c))
	if err != nil {
		fmt.Fprintf(os.Stderr, "刷新失败: %v\n", err)
		return 1
	}
	if err := store.Save(db, toStore(*alias, refreshed)); err != nil {
		fmt.Fprintf(os.Stderr, "保存凭据失败: %v\n", err)
		return 1
	}
	fmt.Printf("已刷新:alias=%s vid=%s 时间=%s\n",
		*alias, refreshed.Vid, time.Now().Format("2006-01-02 15:04:05"))
	return 0
}

func cmdKeepalive(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("keepalive", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	every := fs.Duration("every", 24*time.Hour, "刷新间隔")
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	client := weread.NewClient()

	refreshOnce := func() error {
		// 每轮重新读库,拿到上一轮轮换后的 refreshToken。
		c := loadOrExit(db, *alias)
		refreshed, err := client.Refresh(ctx, toWeread(c))
		if err != nil {
			return err
		}
		return store.Save(db, toStore(*alias, refreshed))
	}
	if err := refreshOnce(); err != nil {
		fmt.Fprintf(os.Stderr, "刷新失败: %v\n", err)
		return 1
	}
	fmt.Printf("keepalive 已启动:每 %s 刷新一次,Ctrl+C 退出\n", *every)
	timer := time.NewTimer(*every)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			fmt.Println("已退出")
			return 0
		case <-timer.C:
			if err := refreshOnce(); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] 刷新失败: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
				return 1
			}
			fmt.Printf("[%s] 已刷新\n", time.Now().Format("2006-01-02 15:04:05"))
			timer.Reset(*every)
		}
	}
}

func cmdServe(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	_, dbPath := commonFlags(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "HTTP 监听地址(默认仅本机可访问)")
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()

	srv := &http.Server{Addr: *addr, Handler: web.New(db).Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	fmt.Printf("Web UI 已启动: http://%s  (Ctrl+C 退出)\n", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "Web 服务失败: %v\n", err)
		return 1
	}
	return 0
}

func cmdShow(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)

	fmt.Printf("alias:        %s\n", c.Alias)
	fmt.Printf("vid:          %s\n", c.Vid)
	fmt.Printf("deviceId:     %s\n", c.DeviceID)
	fmt.Printf("上次刷新:     %s\n", c.RotatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("首次登录:     %s\n", c.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("accessToken缓存: %t\n", c.AccessToken != "")
	return 0
}
