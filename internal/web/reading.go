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
	Enabled bool     `json:"enabled"`
	BookIDs []string `json:"book_ids"`
	Minutes int      `json:"minutes"`
	RunAt   string   `json:"run_at"`
}

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
		if len(body.BookIDs) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New("请至少选择一本书籍"))
			return
		}
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
		"last_run_date": cfg.LastRunDate, "last_run_at": cfg.LastRunAt, "last_status": cfg.LastStatus,
		"run_book_id": cfg.RunBookID, "run_done": cfg.RunDone, "run_total": cfg.RunTotal,
		"running": running,
	})
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
	if len(cfg.BookIDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("请先选择要阅读的书籍并保存配置"))
		return
	}
	// 当日有未完成的会话(误停/中断)则续跑,否则开一场新的。
	var task weread.FarmTask
	if bookID, done, total, ok := cfg.ResumeTask(); ok {
		task = weread.FarmTask{BookID: bookID, Done: done, Total: total}
	} else {
		task = weread.NewFarmTask(cfg.BookIDs, cfg.Minutes)
	}
	if !s.startFarm(vid, task, toWeread(c)) {
		writeErr(w, http.StatusConflict, errors.New("该账号已有阅读会话在进行中"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true, "resumed": task.Done > 0})
}

type farmSessionHandle struct {
	cancel context.CancelFunc
	ctrl   *weread.FarmControl
}

func (s *Server) farmRunning(vid string) bool {
	s.farmMu.Lock()
	defer s.farmMu.Unlock()
	_, ok := s.farms[vid]
	return ok
}

func (s *Server) startFarm(vid string, task weread.FarmTask, creds *weread.Credentials) bool {
	s.farmMu.Lock()
	if _, running := s.farms[vid]; running || s.closing {
		s.farmMu.Unlock()
		return false
	}
	ctrl := weread.NewFarmControl()
	// 会话 ctx 与句柄共享同一个 cancel:「停止」按钮调用它即可终止整场会话。
	sessCtx, cancel := context.WithTimeout(s.ctx, time.Duration(task.Total-task.Done+10)*time.Minute)
	s.farms[vid] = &farmSessionHandle{cancel: cancel, ctrl: ctrl}
	s.farmWG.Add(1)
	s.farmMu.Unlock()

	today := time.Now().Format("2006-01-02")
	// 书名前置到各类日志与状态文案里,日志页一眼可见当前刷的是哪本书。
	book := "《" + s.readingBookTitle(vid, task.BookID) + "》"
	// 先标记归属日期,防止调度器同日重复触发;手动执行也计入当日。
	_ = store.SaveReadingRunState(s.db, vid, today, book+"正在阅读…", &store.RunProgress{BookID: task.BookID, Done: task.Done, Total: task.Total})
	s.logf("info", "farm", vid, "%s阅读会话已启动,目标 %.1f 分钟%s",
		book, float64(task.Total)*0.5, resumeNote(task.Done))

	go func() {
		defer s.farmWG.Done()
		defer func() {
			s.farmMu.Lock()
			delete(s.farms, vid)
			s.farmMu.Unlock()
			cancel()
		}()

		result, next, err := s.client.FarmSession(sessCtx, creds, task, func(done, total int, msg string) {
			// 断点与日志每次心跳(30 秒)记一条,粒度 0.5 分钟。
			_ = store.SaveReadingRunState(s.db, vid, today, fmt.Sprintf("正在阅读%s%.1f/%d 分钟", book, float64(done)*0.5, total/2), &store.RunProgress{BookID: task.BookID, Done: done, Total: total})
			s.logf("info", "farm", vid, "正在阅读%s%.1f/%d 分钟", book, float64(done)*0.5, total/2)
		}, ctrl)
		if next != nil && (next != creds || next.AccessToken != creds.AccessToken || next.RefreshToken != creds.RefreshToken || next.DeviceID != creds.DeviceID || next.Vid != creds.Vid) {
			// 会话中途轮换了移动端凭据,落库,否则会丢会话。
			updated := &store.Credential{
				Vid:          next.Vid,
				RefreshToken: next.RefreshToken,
				DeviceID:     next.DeviceID,
				AccessToken:  next.AccessToken,
			}
			if err := store.Save(s.db, updated); err != nil {
				s.logf("error", "farm", vid, "续期凭据落库失败: %v", err)
			} else {
				s.logf("info", "farm", vid, "会话中凭据已轮换并落库")
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
			status = book + "失败:" + err.Error()
			level = "error"
		case result.Err != "":
			status = fmt.Sprintf("%s中断(已记 %s):%s", book, minutesText, result.Err)
			level = "warn"
		}
		if s.ctx.Err() != nil && !ctrl.Stopped() {
			return
		}
		_ = store.SaveReadingRunState(s.db, vid, today, status, nil)
		s.logf(level, "farm", vid, "%s", status)
		// 完成与失败时推送通知;用户主动停止的会话不打扰。
		switch {
		case ctrl.Stopped():
		case err != nil:
			s.notifyFarmResult(vid, "error", "阅读会话失败", status)
		case result.Err != "":
			s.notifyFarmResult(vid, "warn", "阅读中断", status)
		default:
			s.notifyFarmResult(vid, "info", "今日阅读完成",
				fmt.Sprintf("%s\n已阅读 %s", s.readingBookTitle(vid, task.BookID), minutesText))
		}
	}()
	return true
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
		s.startFarm(cfg.Vid, weread.FarmTask{BookID: bookID, Done: done, Total: total}, toWeread(c))
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
		if len(cfg.BookIDs) == 0 {
			s.logf("warn", "farm", cfg.Vid, "配置未选择书籍,跳过今日调度")
			continue
		}
		c, err := store.Load(s.db, cfg.Vid)
		if err != nil {
			s.logf("error", "farm", cfg.Vid, "凭据加载失败: %v", err)
			continue
		}
		task := weread.NewFarmTask(cfg.BookIDs, cfg.Minutes)
		s.logf("info", "farm", cfg.Vid, "调度器触发每日阅读:%s(计划 %s,目标 %.1f 分钟)",
			"《"+s.readingBookTitle(cfg.Vid, task.BookID)+"》", cfg.RunAt, float64(task.Total)*0.5)
		if !s.startFarm(cfg.Vid, task, toWeread(c)) {
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
