// 阅读挑战的 API 层:配置读写、立即执行、以及 serve 进程内的每日调度器。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

type readingConfigBody struct {
	Enabled    bool     `json:"enabled"`
	BookIDs    []string `json:"book_ids"`
	Minutes    int      `json:"minutes"`
	RunAt      string   `json:"run_at"`
	SkipIfRead *bool    `json:"skip_if_read"` // 缺省时沿用已存值,未存过按默认开启处理
}

// readingSkipIfReadKey 是「当天已读过就跳过」开关在 weread_setting 的键前缀,
// 值为 "1"/"0";未写入时视为开启(issue #134:仅在当日忘记阅读时才补读)。
const readingSkipIfReadKey = "reading_skip_if_read:"

// todayReadSkipMinSeconds 是「当日已读」的判定门槛,与官方「阅读天数」口径一致
// (当日阅读 ≥1 分钟记为 1 天):不足 1 分钟视为没读,照常补读,避免打卡缺天。
const todayReadSkipMinSeconds = 60

func (s *Server) handleReadingConfig(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	if _, err := store.Load(s.db, vid); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if r.Method == http.MethodPost {
		var body readingConfigBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
			return
		}
		if parsed, err := time.Parse("15:04", body.RunAt); err != nil || parsed.Format("15:04") != body.RunAt {
			writeErr(w, http.StatusBadRequest, errors.New("执行时间格式应为 HH:MM,如 03:00"))
			return
		}
		if body.Minutes <= 0 || body.Minutes > 480 {
			writeErr(w, http.StatusBadRequest, errors.New("阅读时长需在 1~480 分钟之间"))
			return
		}
		seen := map[string]bool{}
		ids := []string{}
		for _, id := range body.BookIDs {
			id = strings.TrimSpace(id)
			if id == "" || strings.ContainsAny(id, ",\r\n") {
				writeErr(w, 400, errors.New("书籍 ID 无效"))
				return
			}
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		body.BookIDs = ids
		// 书单允许为空:留空即「每天从书架随机挑一本」,调度与会话启动时实时解析。
		if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{
			Vid:     vid,
			Enabled: body.Enabled,
			BookIDs: body.BookIDs,
			Minutes: body.Minutes,
			RunAt:   body.RunAt,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if body.SkipIfRead != nil {
			v := "0"
			if *body.SkipIfRead {
				v = "1"
			}
			if err := store.SetSetting(s.db, readingSkipIfReadKey+vid, v); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
	}

	cfg, err := store.GetReadingConfig(s.db, vid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	running := s.farmRunning(vid)
	writeJSON(w, http.StatusOK, map[string]any{
		"vid": cfg.Vid, "enabled": cfg.Enabled, "book_ids": cfg.BookIDs,
		"minutes": cfg.Minutes, "run_at": cfg.RunAt,
		"skip_if_read":  s.readingSkipIfReadOn(vid),
		"last_run_date": cfg.LastRunDate, "last_run_at": cfg.LastRunAt, "last_status": cfg.LastStatus,
		"run_book_id": cfg.RunBookID, "run_done": cfg.RunDone, "run_total": cfg.RunTotal,
		"running": running,
	})
}

func (s *Server) readingSkipIfReadOn(vid string) bool {
	if v, err := store.GetSetting(s.db, readingSkipIfReadKey+vid); err == nil {
		return v == "1"
	}
	return true
}

// todayAlreadyRead 查询账号今天的阅读秒数(weekReadDaysDetail,与官方口径同源)。
// ok=false 表示无法判定(查询失败或明细缺失):调用方按未读处理(fail-open),
// 宁可多读不可漏读:工具的本职是保住打卡,不能因判定失败反而漏掉一天。
func (s *Server) todayAlreadyRead(ctx context.Context, vid string) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, _, err := s.weeklyCall(ctx, vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.WeeklyExchange(ctx, creds, 0, 0, 0)
	})
	if err != nil {
		return 0, false
	}
	var rw weread.WeeklyRewards
	if json.Unmarshal(data, &rw) != nil {
		return 0, false
	}
	return rw.TodayReadSeconds(time.Now())
}

// shelfBookIDs 拉取书架的可读书目 ID 列表,供「未选书单」时随机挑一本。
// 会话过期时自动续期并重试一次,轮换出的凭据经 client.OnRefresh 落库;
// ShelfSync 已过滤听书专辑等无 bookId 的条目。
func (s *Server) shelfBookIDs(ctx context.Context, vid string) ([]string, error) {
	c, err := store.Load(s.db, vid)
	if err != nil {
		return nil, err
	}
	creds := toWeread(c)
	shelf, err := s.client.ShelfSync(ctx, creds)
	if errors.Is(err, weread.ErrSessionExpired) {
		next, rerr := s.client.Refresh(ctx, creds)
		if rerr != nil {
			return nil, fmt.Errorf("会话已过期且续期失败: %w", rerr)
		}
		shelf, err = s.client.ShelfSync(ctx, next)
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(shelf.Books))
	for _, b := range shelf.Books {
		ids = append(ids, b.BookID)
	}
	return ids, nil
}

// handleReadingRun 立即执行一次阅读会话(异步),执行状态写库,前端轮询配置接口可见。
func (s *Server) handleReadingRun(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	c, err := store.Load(s.db, vid)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cfg, err := store.GetReadingConfig(s.db, vid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// 当日有未完成的会话(误停/中断)则续跑,否则开一场新的。
	bookIDs := cfg.BookIDs
	if len(bookIDs) == 0 {
		// 未选书单:从书架随机挑一本(实时拉取,保证新书也能选到)。
		ids, err := s.shelfBookIDs(r.Context(), vid)
		if err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("书架获取失败: %w", err))
			return
		}
		if len(ids) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New("书架为空,无法随机选择书籍"))
			return
		}
		bookIDs = ids
	}
	var task weread.FarmTask
	if bookID, done, total, ok := cfg.ResumeTask(); ok {
		task = weread.FarmTask{BookID: bookID, Done: done, Total: total}
	} else {
		task = weread.NewFarmTask(bookIDs, cfg.Minutes)
	}
	if err := s.startFarm(vid, task, toWeread(c)); err != nil {
		if errors.Is(err, errFarmAlreadyRunning) {
			writeErr(w, http.StatusConflict, err)
		} else {
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true, "resumed": task.Done > 0})
}

type farmSessionHandle struct {
	cancel context.CancelFunc
	ctrl   *weread.FarmControl
}

var errFarmAlreadyRunning = errors.New("该账号已有阅读会话在进行中")

func (s *Server) farmRunning(vid string) bool {
	s.farmMu.Lock()
	defer s.farmMu.Unlock()
	_, ok := s.farms[vid]
	return ok
}

func (s *Server) startFarm(vid string, task weread.FarmTask, creds *weread.Credentials) error {
	s.farmMu.Lock()
	if _, running := s.farms[vid]; running || s.closing {
		s.farmMu.Unlock()
		return errFarmAlreadyRunning
	}
	ctrl := weread.NewFarmControl()
	// 会话 ctx 与句柄共享同一个 cancel:「停止」按钮调用它即可终止整场会话,
	// 包括失败重试的等待期;单次尝试的超时在循环内按剩余进度单独计算。
	runCtx, runCancel := context.WithCancel(s.ctx)
	s.farms[vid] = &farmSessionHandle{cancel: runCancel, ctrl: ctrl}
	s.farmWG.Add(1)
	s.farmMu.Unlock()

	today := time.Now().Format("2006-01-02")
	// 书名前置到各类日志与状态文案里,日志页一眼可见当前刷的是哪本书。
	book := "《" + s.readingBookTitle(vid, task.BookID) + "》"
	// 先标记归属日期,防止调度器同日重复触发;手动执行也计入当日。
	if err := store.SaveReadingRunState(s.db, vid, today, book+"正在阅读…", &store.RunProgress{BookID: task.BookID, Done: task.Done, Total: task.Total}); err != nil {
		s.farmMu.Lock()
		delete(s.farms, vid)
		s.farmMu.Unlock()
		runCancel()
		s.farmWG.Done()
		return fmt.Errorf("保存阅读任务状态失败: %w", err)
	}
	s.logf("info", "farm", vid, "%s阅读会话已启动,目标 %.1f 分钟%s",
		book, float64(task.Total)*0.5, resumeNote(task.Done))

	go func() {
		defer s.farmWG.Done()
		defer func() {
			s.farmMu.Lock()
			delete(s.farms, vid)
			s.farmMu.Unlock()
			runCancel()
		}()

		// 会话中途轮换的凭据已由 client.OnRefresh 即时落库。
		// 会话失败(网络等)自动重试:间隔 5 分钟,最多 2 次,重试期间不打扰推送;
		// 中断(服务端中途拒绝)可能是风控信号,不重试,保留进度交用户处理。
		const maxRetries = 2
		var result *weread.FarmResult
		var err error
		retries := 0
		for {
			attemptCtx, attemptCancel := context.WithTimeout(runCtx, time.Duration(task.Total-task.Done+10)*time.Minute)
			result, _, err = s.client.FarmSession(attemptCtx, creds, task, func(done, total int, msg string) {
				// 断点与日志每次心跳(30 秒)记一条,粒度 0.5 分钟。
				if err := store.SaveReadingRunState(s.db, vid, today, fmt.Sprintf("正在阅读%s%.1f/%d 分钟", book, float64(done)*0.5, total/2), &store.RunProgress{BookID: task.BookID, Done: done, Total: total}); err != nil {
					s.logf("error", "farm", vid, "阅读进度保存失败: %v", err)
				}
				s.logf("info", "farm", vid, "正在阅读%s%.1f/%d 分钟", book, float64(done)*0.5, total/2)
			}, ctrl)
			attemptCancel()
			if err == nil || retries >= maxRetries || ctrl.Stopped() || runCtx.Err() != nil {
				break
			}
			retries++
			task.Done = result.Done // 保留已记录进度续跑
			note := fmt.Sprintf("%s会话失败(%s),5 分钟后第 %d/%d 次重试", book, err, retries, maxRetries)
			s.logf("warn", "farm", vid, "%s", note)
			_ = store.SaveReadingRunState(s.db, vid, today, note, &store.RunProgress{BookID: task.BookID, Done: task.Done, Total: task.Total})
			select {
			case <-runCtx.Done():
				// 等待期间被停止或服务退出:交由下方既有分支收尾。
			case <-time.After(5 * time.Minute):
			}
		}
		minutesText := fmt.Sprintf("%.1f 分钟", float64(result.Done)*0.5)
		status := fmt.Sprintf("%s完成:阅读 %s", book, minutesText)
		level := "info"
		switch {
		case ctrl.Stopped():
			status = fmt.Sprintf("%s已停止(已记 %s)", book, minutesText)
			level = "warn"
		case err != nil:
			status = fmt.Sprintf("%s失败(已重试 %d 次):%v", book, retries, err)
			level = "error"
		case result.Err != "":
			status = fmt.Sprintf("%s中断(已记 %s):%s", book, minutesText, result.Err)
			level = "warn"
		}
		if s.ctx.Err() != nil && !ctrl.Stopped() {
			return
		}
		if err := store.SaveReadingRunState(s.db, vid, today, status, nil); err != nil {
			s.logf("error", "farm", vid, "阅读结果保存失败: %v", err)
		}
		s.logf(level, "farm", vid, "%s", status)
		// 完成与失败时推送通知;用户主动停止的会话不打扰。
		switch {
		case ctrl.Stopped():
		case err != nil:
			s.notifyFarmResult(vid, "error", "阅读会话失败",
				fmt.Sprintf("%s自动阅读失败(已重试 %d 次):%v;明日将自动重试", book, retries, err))
		case result.Err != "":
			s.notifyFarmResult(vid, "warn", "阅读中断",
				fmt.Sprintf("%s记录 %s 后中断:%s;进度已保留,可在页面继续", book, minutesText, result.Err))
		default:
			body := fmt.Sprintf("%s\n已阅读 %s", s.readingBookTitle(vid, task.BookID), minutesText)
			// 账号勾选了周奖励偏好时,顺手把本周奖励状态并进同一条推送(一次查询)。
			if reward := s.weeklyRewardSummary(vid); reward != "" {
				body += "\n\n本周奖励:\n" + reward
			}
			s.notifyFarmResult(vid, "info", "今日阅读完成", body)
		}
	}()
	return nil
}

// ResumeInterrupted 在服务启动时仅续跑进程异常退出后留下的当日未完成会话。
// 用户主动停止或上报失败会清除断点，并视为当日任务结束。
func (s *Server) ResumeInterrupted() {
	cfgs, err := store.ListEnabledReadingConfigs(s.db)
	if err != nil {
		return
	}
	for _, cfg := range cfgs {
		bookID, done, total, ok := cfg.ResumeTask()
		if !ok {
			continue
		}
		c, err := store.Load(s.db, cfg.Vid)
		if err != nil {
			continue
		}
		s.logf("info", "farm", cfg.Vid, "《%s》续跑上次未完成的阅读会话(已完成 %.1f/%.1f 分钟)",
			s.readingBookTitle(cfg.Vid, bookID), float64(done)*0.5, float64(total)*0.5)
		if err := s.startFarm(cfg.Vid, weread.FarmTask{BookID: bookID, Done: done, Total: total}, toWeread(c)); err != nil && !errors.Is(err, errFarmAlreadyRunning) {
			s.logf("error", "farm", cfg.Vid, "续跑阅读任务失败: %v", err)
		}
	}
}

// handleReadingStop 终止进行中的阅读会话。已上报的时长服务端已记账,不会回滚;
// 当日调度视为已完成,明天到点再跑。
func (s *Server) handleReadingStop(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	s.farmMu.Lock()
	h, ok := s.farms[vid]
	s.farmMu.Unlock()
	if !ok {
		writeErr(w, http.StatusConflict, errors.New("没有进行中的阅读会话"))
		return
	}
	h.ctrl.Stop()
	h.cancel()
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

// StartFarmScheduler 启动每日调度循环:每 30 秒扫描一次启用了阅读挑战的账号,
// 到达当日执行时间且今天还没跑过的就开一场阅读会话。错过时间点(如进程
// 中午才启动、计划在凌晨)会在启动后补跑当日场次。
func (s *Server) StartFarmScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tickFarm(ctx)
			}
		}
	}()
}

func (s *Server) tickFarm(ctx context.Context) {
	cfgs, err := store.ListEnabledReadingConfigs(s.db)
	if err != nil {
		s.logf("error", "farm", "", "读取配置失败: %v", err)
		return
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	nowHM := now.Format("15:04")
	for _, cfg := range cfgs {
		if ctx.Err() != nil {
			return
		}
		if cfg.LastRunDate == today {
			continue
		}
		if nowHM < cfg.RunAt {
			continue
		}
		// 当日已读判定:已读就不代读,查询失败按未读处理照常执行(fail-open)。
		if s.readingSkipIfReadOn(cfg.Vid) {
			if sec, ok := s.todayAlreadyRead(ctx, cfg.Vid); ok && sec >= todayReadSkipMinSeconds {
				status := fmt.Sprintf("今日已阅读 %.1f 分钟,跳过自动阅读", float64(sec)/60)
				// 记下当日已处理,否则每 30 秒扫描都会重复查询与写这条状态。
				if err := store.SaveReadingRunState(s.db, cfg.Vid, today, status, nil); err != nil {
					s.logf("error", "farm", cfg.Vid, "保存调度状态失败: %v", err)
				}
				s.logf("info", "farm", cfg.Vid, "%s", status)
				continue
			}
		}
		bookIDs := cfg.BookIDs
		if len(bookIDs) == 0 {
			// 未选书单:从书架随机挑一本(每天一换,实时拉取)。
			// 获取失败/书架为空是持续状态,记下当日已处理,否则每 30 秒扫描都会重复打请求与告警。
			ids, err := s.shelfBookIDs(ctx, cfg.Vid)
			if err != nil || len(ids) == 0 {
				status := "书架为空,跳过今日调度"
				if err != nil {
					status = fmt.Sprintf("书架获取失败(%v),跳过今日调度", err)
				}
				if err := store.SaveReadingRunState(s.db, cfg.Vid, today, status, nil); err != nil {
					s.logf("error", "farm", cfg.Vid, "保存调度状态失败: %v", err)
				}
				s.logf("warn", "farm", cfg.Vid, "%s", status)
				continue
			}
			bookIDs = ids
			s.logf("info", "farm", cfg.Vid, "未选择书单,今日从书架随机选一本(共 %d 本可选)", len(ids))
		}
		c, err := store.Load(s.db, cfg.Vid)
		if err != nil {
			s.logf("error", "farm", cfg.Vid, "凭据加载失败: %v", err)
			continue
		}
		task := weread.NewFarmTask(bookIDs, cfg.Minutes)
		s.logf("info", "farm", cfg.Vid, "调度器触发每日阅读:%s(计划 %s,目标 %.1f 分钟)",
			"《"+s.readingBookTitle(cfg.Vid, task.BookID)+"》", cfg.RunAt, float64(task.Total)*0.5)
		if err := s.startFarm(cfg.Vid, task, toWeread(c)); err != nil {
			if !errors.Is(err, errFarmAlreadyRunning) {
				s.logf("error", "farm", cfg.Vid, "启动阅读任务失败: %v", err)
			}
			continue
		}
	}
}

// resumeNote 续跑时在启动日志里标注断点。
func resumeNote(done int) string {
	if done > 0 {
		return fmt.Sprintf("(续跑,已完成 %.1f 分钟)", float64(done)*0.5)
	}
	return ""
}

// Close 先结束后台阅读会话，保留断点后再允许调用方关闭数据库。
func (s *Server) Close() {
	s.farmMu.Lock()
	s.closing = true
	s.cancel()
	s.farmMu.Unlock()
	s.farmWG.Wait()
}
