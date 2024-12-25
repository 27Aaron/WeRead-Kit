// 命令 wxread:终端扫码登录微信读书,凭据存 SQLite,并自动续期保持长期可用。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	case "shelf":
		code = cmdShelf(ctx, os.Args[2:])
	case "farm-run":
		code = cmdFarmRun(ctx, os.Args[2:])
	case "info":
		code = cmdInfo(ctx, os.Args[2:])
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
  wxread shelf     [--alias 别名]                  列出书架中的书籍
  wxread info      [--alias 别名]                  查看用户信息与会员卡
  wxread farm-run    [--alias 别名] [--minutes N]   立即执行一次阅读会话(默认读 30 分钟)
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
	// 登录即预热详情缓存(用户信息/会员卡/书架),Web 详情页首开秒出。
	// 失败只提示,不影响登录结果。
	fmt.Println("正在缓存账号资料与书架……")
	if d, next, err := weread.NewClient().Details(ctx, creds); err != nil {
		fmt.Fprintf(os.Stderr, "资料缓存失败(不影响登录,可稍后在 Web UI 里刷新): %v\n", err)
	} else {
		shelfJSON, _ := json.Marshal(d.Shelf)
		if cerr := store.SaveDetailsCache(db, *alias, d.User, d.Card, shelfJSON); cerr != nil {
			fmt.Fprintf(os.Stderr, "资料缓存写入失败: %v\n", cerr)
		}
		if next != nil {
			_ = store.Save(db, toStore(*alias, next))
		}
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

// runWithRefresh 执行 run;若返回会话过期,先用 refreshToken 续期落库,再重试一次。
func runWithRefresh(ctx context.Context, client *weread.Client, db *sql.DB, alias string, c *store.Credential, run func(*weread.Credentials) error) error {
	err := run(toWeread(c))
	if !errors.Is(err, weread.ErrSessionExpired) {
		return err
	}
	refreshed, rerr := client.Refresh(ctx, toWeread(c))
	if rerr != nil {
		return fmt.Errorf("续期失败: %w", rerr)
	}
	if serr := store.Save(db, toStore(alias, refreshed)); serr != nil {
		return serr
	}
	return run(refreshed)
}

func cmdShelf(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("shelf", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)
	client := weread.NewClient()

	var shelf *weread.ShelfSync
	err := runWithRefresh(ctx, client, db, *alias, c, func(creds *weread.Credentials) (err error) {
		shelf, err = client.ShelfSync(ctx, creds)
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取书架失败: %v\n", err)
		return 1
	}
	fmt.Printf("书架共 %d 本:\n", len(shelf.Books))
	for i, b := range shelf.Books {
		title := b.Title
		if title == "" {
			title = b.BookID
		}
		author := b.Author
		if author != "" {
			author = " — " + author
		}
		fmt.Printf("%3d. %s%s\n", i+1, title, author)
	}
	return 0
}

func cmdInfo(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)
	client := weread.NewClient()

	var cookie string
	if err := runWithRefresh(ctx, client, db, *alias, c, func(creds *weread.Credentials) (err error) {
		cookie, err = client.WebCookie(ctx, creds)
		return err
	}); err != nil {
		fmt.Fprintf(os.Stderr, "桥接网页会话失败: %v\n", err)
		return 1
	}

	type section struct {
		name string
		raw  json.RawMessage
	}
	var sections []section
	fetch := map[string]func() (json.RawMessage, error){
		"用户信息": func() (json.RawMessage, error) { return client.WebUserInfo(ctx, cookie, c.Vid) },
		"会员卡":  func() (json.RawMessage, error) { return client.WebMemberCard(ctx, cookie) },
	}
	failed := 0
	for _, name := range []string{"用户信息", "会员卡"} {
		raw, err := fetch[name]()
		if errors.Is(err, weread.ErrSessionExpired) {
			// 网页会话半路过期:重新桥接再试一次这一节。
			if cerr := runWithRefresh(ctx, client, db, *alias, c, func(creds *weread.Credentials) (err error) {
				cookie, err = client.WebCookie(ctx, creds)
				return err
			}); cerr == nil {
				raw, err = fetch[name]()
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "== %s == 获取失败: %v\n", name, err)
			failed++
			continue
		}
		sections = append(sections, section{name, raw})
	}
	for _, sec := range sections {
		pretty, err := json.MarshalIndent(json.RawMessage(sec.raw), "", "  ")
		if err != nil {
			pretty = sec.raw
		}
		fmt.Printf("== %s ==\n%s\n\n", sec.name, pretty)
	}
	if failed == 3 {
		return 1
	}
	return 0
}

// cmdFarmRun 立即执行一次阅读会话,给不开 serve 常驻、想用 cron 的用户。
// 配置(选书/时长)读取已保存的阅读配置;--minutes 可临时覆盖。
func cmdFarmRun(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("farm-run", flag.ExitOnError)
	alias, dbPath := commonFlags(fs)
	minutes := fs.Int("minutes", 0, "覆盖配置的阅读时长(分钟)")
	_ = fs.Parse(args)

	db := mustOpenDB(*dbPath)
	defer db.Close()
	c := loadOrExit(db, *alias)
	cfg, err := store.GetReadingConfig(db, *alias)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取阅读配置失败: %v\n", err)
		return 1
	}
	if len(cfg.BookIDs) == 0 {
		fmt.Fprintln(os.Stderr, "尚未配置阅读书籍:先在 Web UI 里选书保存,或说明见 README")
		return 1
	}
	target := cfg.Minutes
	if *minutes > 0 {
		target = *minutes
	}

	client := weread.NewClient()
	fmt.Printf("开始阅读:《书架所选》目标 %d 分钟(每 30 秒记 0.5 分钟)……\n", target)
	result, next, err := client.FarmSession(ctx, toWeread(c), cfg.BookIDs, target, func(done, total int, msg string) {
		fmt.Printf("  进度 %d/%d:%s\n", done, total, msg)
	})
	if next != nil {
		_ = store.Save(db, toStore(*alias, next))
	}
	if result != nil {
		fmt.Printf("阅读了 %.1f 分钟(记 %d/%d 次心跳)\n", float64(result.Heartbeats)*0.5, result.Heartbeats, target*2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "阅读会话失败: %v\n", err)
		return 1
	}
	if result.Err != "" {
		fmt.Fprintf(os.Stderr, "阅读会话中断: %s\n", result.Err)
		return 1
	}
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

	webServer := web.New(db)
	webServer.StartFarmScheduler(ctx)
	store.AddLog(db, "info", "serve", "", "Web 服务已启动,监听 "+*addr)
	srv := &http.Server{Addr: *addr, Handler: webServer.Handler()}
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
