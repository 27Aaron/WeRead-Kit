// 自动阅读的 API 层:配置读写、立即执行、以及 serve 进程内的每日调度器。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"wxread/internal/store"
	"wxread/internal/weread"
)

type readingConfigBody struct {
	Enabled bool     `json:"enabled"`
	BookIDs []string `json:"book_ids"`
	Minutes int      `json:"minutes"`
	RunAt   string   `json:"run_at"`
}

func (s *Server) handleReadingConfig(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	if _, err := store.Load(s.db, alias); errors.Is(err, store.ErrNotFound) {
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
		if _, err := time.Parse("15:04", body.RunAt); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("执行时间格式应为 HH:MM,如 03:00"))
			return
		}
		if body.Minutes <= 0 || body.Minutes > 480 {
			writeErr(w, http.StatusBadRequest, errors.New("阅读时长需在 1~480 分钟之间"))
			return
		}
		if body.Enabled && len(body.BookIDs) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New("启用自动阅读前请至少选择一本书"))
			return
		}
		if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{
			Alias:   alias,
			Enabled: body.Enabled,
			BookIDs: body.BookIDs,
			Minutes: body.Minutes,
			RunAt:   body.RunAt,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	cfg, err := store.GetReadingConfig(s.db, alias)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleReadingRun 立即执行一次阅读会话(异步),执行状态写库,前端轮询配置接口可见。
func (s *Server) handleReadingRun(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	c, err := store.Load(s.db, alias)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cfg, err := store.GetReadingConfig(s.db, alias)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if len(cfg.BookIDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("请先选择要阅读的书籍并保存配置"))
		return
	}
	if !s.startFarm(alias, cfg, toWeread(c)) {
		writeErr(w, http.StatusConflict, errors.New("该账号已有阅读会话在进行中"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true})
}

// farmRunner 是一个账号当前是否在跑阅读会话的内存标记。
var farmRunning = struct {
	sync.Mutex
	set map[string]bool
}{set: map[string]bool{}}

func (s *Server) startFarm(alias string, cfg *store.ReadingConfig, creds *weread.Credentials) bool {
	farmRunning.Lock()
	if farmRunning.set[alias] {
		farmRunning.Unlock()
		return false
	}
	farmRunning.set[alias] = true
	farmRunning.Unlock()

	today := time.Now().Format("2006-01-02")
	// 先标记归属日期,防止调度器同日重复触发;手动执行也计入当日。
	_ = store.SaveReadingRunState(s.db, alias, today, "阅读中…")
	s.logf("info", "farm", alias, "阅读会话已启动,目标 %d 分钟(心跳 %d 次)", cfg.Minutes, cfg.Minutes*2)

	go func() {
		defer func() {
			farmRunning.Lock()
			delete(farmRunning.set, alias)
			farmRunning.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Minutes+10)*time.Minute)
		defer cancel()

		result, next, err := s.client.FarmSession(ctx, creds, cfg.BookIDs, cfg.Minutes, func(done, total int, msg string) {
			_ = store.SaveReadingRunState(s.db, alias, today, fmt.Sprintf("阅读中 %d/%d 分钟", done/2, total/2))
			if done%10 == 0 {
				s.logf("info", "farm", alias, "阅读进度 %d/%d 分钟", done/2, total/2)
			}
		})
		if next != nil {
			// 会话中途轮换了移动端凭据,落库,否则会丢会话。
			updated := &store.Credential{
				Alias: alias, Vid: next.Vid, RefreshToken: next.RefreshToken,
				DeviceID: next.DeviceID, AccessToken: next.AccessToken,
			}
			if err := store.Save(s.db, updated); err != nil {
				s.logf("error", "farm", alias, "续期凭据落库失败: %v", err)
			} else {
				s.logf("info", "farm", alias, "会话中凭据已轮换并落库")
			}
		}
		minutesText := fmt.Sprintf("%.1f 分钟", float64(result.Heartbeats)*0.5)
		status := fmt.Sprintf("完成:阅读 %s(记 %d/%d 次心跳)", minutesText, result.Heartbeats, cfg.Minutes*2)
		level := "info"
		if err != nil {
			status = "失败:" + err.Error()
			level = "error"
		} else if result.Err != "" {
			status = fmt.Sprintf("中断(已记 %s):%s", minutesText, result.Err)
			level = "warn"
		}
		_ = store.SaveReadingRunState(s.db, alias, today, status)
		s.logf(level, "farm", alias, "%s", status)
	}()
	return true
}

// StartFarmScheduler 启动每日调度循环:每 30 秒扫描一次启用了自动阅读的账号,
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
	today := time.Now().Format("2006-01-02")
	nowHM := time.Now().Format("15:04")
	for _, cfg := range cfgs {
		if cfg.LastRunDate == today {
			continue
		}
		if nowHM < cfg.RunAt {
			continue
		}
		c, err := store.Load(s.db, cfg.Alias)
		if err != nil {
			s.logf("error", "farm", cfg.Alias, "凭据加载失败: %v", err)
			continue
		}
		s.logf("info", "farm", cfg.Alias, "调度器触发每日阅读(计划 %s,目标 %d 分钟)", cfg.RunAt, cfg.Minutes)
		if !s.startFarm(cfg.Alias, cfg, toWeread(c)) {
			continue
		}
	}
}
