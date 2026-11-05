// 「我的阅读」页 API:体验卡活动的自动领取开关与调度。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// actAutoClaimKey 是「自动领取体验卡活动」开关在 weread_setting 的键前缀,值为 "1"/"0"。
const actAutoClaimKey = "act_auto_claim:"

// handleActAutoClaim 保存自动领取开关。body: {"enabled":true}
// 开启后调度器周期扫描送体验卡活动(热门书单、飙升榜),有可领的自动领取。
// POST /api/accounts/{vid}/act/auto-claim
func (s *Server) handleActAutoClaim(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
		return
	}
	if _, err := store.Load(s.db, vid); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	v := "0"
	if body.Enabled {
		v = "1"
	}
	if err := store.SetSetting(s.db, actAutoClaimKey+vid, v); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	state := "关闭"
	if body.Enabled {
		state = "开启"
	}
	s.logf("info", "reading", vid, "体验卡活动自动领取已%s:扫描热门书单/飙升榜,有可领的自动领取", state)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}

// actAutoClaimOn 读取账号的自动领取开关。
func (s *Server) actAutoClaimOn(vid string) bool {
	v, err := store.GetSetting(s.db, actAutoClaimKey+vid)
	return err == nil && v == "1"
}

// StartSendInfiniteScheduler 启动体验卡活动自动领取循环:启动 2 分钟后首轮,
// 此后每 30 分钟扫描一遍开启开关的账号。活动按天重置,半小时粒度足够及时。
func (s *Server) StartSendInfiniteScheduler(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
		s.claimSendInfiniteOnce(ctx)
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.claimSendInfiniteOnce(ctx)
			}
		}
	}()
}

// claimSendInfiniteOnce 执行一轮:对每个开启开关的账号,逐个活动查询,
// 可领即领并记日志。失败仅记日志,下一轮自然重试。
func (s *Server) claimSendInfiniteOnce(ctx context.Context) {
	accounts, err := store.List(s.db)
	if err != nil {
		return
	}
	for _, acc := range accounts {
		if ctx.Err() != nil {
			return
		}
		if !s.actAutoClaimOn(acc.Vid) {
			continue
		}
		for _, act := range weread.SendInfiniteActivities {
			if ctx.Err() != nil {
				return
			}
			s.claimSendInfiniteFor(ctx, acc.Vid, act)
		}
	}
}

// claimSendInfiniteFor 处理单个账号的单个活动:查询(带续期)→ 可领则领取。
func (s *Server) claimSendInfiniteFor(ctx context.Context, vid string, act weread.SendInfiniteActivity) {
	var state weread.SendInfiniteState
	if _, _, err := s.weeklyCall(ctx, vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		st, err := s.client.SendInfiniteQuery(ctx, creds, act)
		if err != nil {
			return nil, err
		}
		state = st
		return json.RawMessage(`{}`), nil
	}); err != nil {
		s.logf("warn", "reading", vid, "体验卡活动(%s)查询失败: %v", act.Name, err)
		return
	}
	if !state.Claimable() {
		return
	}
	if _, _, err := s.weeklyCall(ctx, vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.SendInfiniteReceive(ctx, creds, act)
	}); err != nil {
		s.logf("warn", "reading", vid, "体验卡活动(%s)领取失败: %v", act.Name, err)
		return
	}
	days := state.Days()
	s.logf("info", "reading", vid, "自动领取体验卡:「%s」+ %d 天", act.Name, days)
}
