// 「阅读挑战」页 API:官方挑战赛的进度查询、报名与自动参与。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// challengeAutoJoinKey 是「自动参与下一期挑战」开关在 weread_setting 的键前缀,
// 值为 "1"/"0";challengeNotifiedStartKey 记录已推送完成通知的那期挑战 startTime,
// 用于跨轮去重。challengeJoinBackoff 是报名/查询失败后的冷却时长。
const (
	challengeAutoJoinKey      = "challenge_auto_join:"
	challengeNotifiedStartKey = "challenge_notified_start:"
	challengeJoinBackoff      = time.Hour
)

// handleChallengeDetail 返回指定账号的官方挑战赛进度详情(version=v3,
// challengeList[0] 为进行中的那一场,未报名时为空数组)与自动参与开关。
// GET /api/accounts/{vid}/challenge
func (s *Server) handleChallengeDetail(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	data, _, err := s.weeklyCall(r.Context(), vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.ChallengeDetail(r.Context(), creds)
	})
	if err != nil {
		writeWeeklyErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vid":       vid,
		"challenge": json.RawMessage(data),
		"auto_join": s.challengeAutoJoinOn(vid),
	})
}

// handleChallengeAutoJoin 保存自动参与开关。body: {"enabled":true}
// 开启后由调度器周期检查:没有进行中的官方挑战时自动报名免费的 21 天挑战。
// POST /api/accounts/{vid}/challenge/auto-join
func (s *Server) handleChallengeAutoJoin(w http.ResponseWriter, r *http.Request) {
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
	if err := store.SetSetting(s.db, challengeAutoJoinKey+vid, v); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	state := "关闭"
	if body.Enabled {
		state = "开启"
	}
	s.logf("info", "reading", vid, "挑战自动参与已%s:无进行中的官方挑战时自动报名 21 天免费挑战", state)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}

// challengeAutoJoinOn 读取账号的自动参与开关。
func (s *Server) challengeAutoJoinOn(vid string) bool {
	v, err := store.GetSetting(s.db, challengeAutoJoinKey+vid)
	return err == nil && v == "1"
}

// handleChallengeHistory 返回指定账号的往期挑战战绩列表。
// GET /api/accounts/{vid}/challenge/history
func (s *Server) handleChallengeHistory(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	data, _, err := s.weeklyCall(r.Context(), vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.ChallengeHistory(r.Context(), creds)
	})
	if err != nil {
		writeWeeklyErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vid":     vid,
		"history": json.RawMessage(data),
	})
}

// handleChallengeJoin 报名免费的 21 天阅读挑战。body: {"challenge_id":"ChallengeLongTermFree"}
// challenge_id 走白名单:付费挑战需要应用内支付,纯接口报名只会被服务端拒绝。
// POST /api/accounts/{vid}/challenge/join
func (s *Server) handleChallengeJoin(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	var body struct {
		ChallengeID string `json:"challenge_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
		return
	}
	if body.ChallengeID != weread.ChallengeLongTermFree {
		writeErr(w, http.StatusBadRequest, errors.New("参数无效:challenge_id 仅支持 ChallengeLongTermFree(21 天免费挑战)"))
		return
	}
	data, _, err := s.weeklyCall(r.Context(), vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.ChallengeJoin(r.Context(), creds, body.ChallengeID)
	})
	if err != nil {
		if errors.Is(err, weread.ErrChallengeAlreadyJoined) {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeWeeklyErr(w, err)
		return
	}
	s.logf("info", "reading", vid, "已报名 21 天阅读挑战(免费,完成自动发勋章)")
	writeJSON(w, http.StatusOK, map[string]any{
		"vid":    vid,
		"result": json.RawMessage(data),
	})
}

// StartChallengeAutoJoinScheduler 启动挑战自动参与循环:启动 1 分钟后首轮,
// 此后每 5 分钟扫描开启开关的账号。与周奖励调度器同模式,独立成环互不阻塞。
func (s *Server) StartChallengeAutoJoinScheduler(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		s.autoJoinChallenges(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.autoJoinChallenges(ctx)
			}
		}
	}()
}

// autoJoinChallenges 执行一轮自动参与:查当前挑战,无进行中的则报名下一期;
// 检测到挑战完成时推送通知。失败按账号退避,避免每 5 分钟重复打失败请求。
func (s *Server) autoJoinChallenges(ctx context.Context) {
	accounts, err := store.List(s.db)
	if err != nil {
		return
	}
	for _, acc := range accounts {
		if ctx.Err() != nil {
			return
		}
		if !s.challengeAutoJoinOn(acc.Vid) {
			continue
		}
		if s.challengeBackoffUntil(acc.Vid).After(time.Now()) {
			continue
		}
		s.autoJoinAccount(ctx, acc.Vid)
	}
}

// autoJoinAccount 处理单个账号的一轮检查。
func (s *Server) autoJoinAccount(ctx context.Context, vid string) {
	data, _, err := s.weeklyCall(ctx, vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.ChallengeDetail(ctx, creds)
	})
	if err != nil {
		s.setChallengeBackoff(vid)
		s.logf("warn", "reading", vid, "挑战自动参与:详情查询失败(%v),1 小时后重试", err)
		return
	}
	prog, has, err := weread.ParseChallengeDetail(data)
	if err != nil {
		return // 响应形态异常,静默跳过等待下一轮
	}
	if has {
		s.notifyChallengeDone(vid, prog)
		if prog.Status == weread.ChallengeStatusInProgress {
			return
		}
	}
	// 走到这里说明未报名或挑战已结束:报名下一期。
	_, _, err = s.weeklyCall(ctx, vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.ChallengeJoin(ctx, creds, weread.ChallengeLongTermFree)
	})
	if errors.Is(err, weread.ErrChallengeAlreadyJoined) {
		return // 服务端已有进行中的挑战,本地视图下一轮会追上
	}
	if err != nil {
		s.setChallengeBackoff(vid)
		s.logf("warn", "reading", vid, "挑战自动参与:报名失败(%v),1 小时后重试", err)
		return
	}
	s.logf("info", "reading", vid, "挑战自动参与:已自动报名 21 天阅读挑战")
	s.notifyFarmResult(vid, "info", "已自动报名 21 天阅读挑战", "检测到没有进行中的官方挑战,已自动报名下一期(免费);勋章与奖励完成后由官方自动发放。")
}

// notifyChallengeDone 在检测到挑战完成(SUCCEEDED)时推送一次通知;
// 用该期挑战的 startTime 去重,同一期只通知一次,重启后也不重复。
func (s *Server) notifyChallengeDone(vid string, prog weread.ChallengeProgress) {
	if prog.Status != weread.ChallengeStatusSucceeded {
		return
	}
	key := challengeNotifiedStartKey + vid
	start := strconv.FormatInt(prog.StartTime, 10)
	if v, err := store.GetSetting(s.db, key); err == nil && v == start {
		return
	}
	if err := store.SetSetting(s.db, key, start); err != nil {
		return
	}
	s.logf("info", "reading", vid, "挑战自动参与:21 天阅读挑战已完成,勋章与奖励已发放")
	s.notifyFarmResult(vid, "info", "21 天阅读挑战完成", "本期挑战已完成,勋章与奖励已由官方发放;下一期将自动报名。")
}

// setChallengeBackoff / challengeBackoffUntil 维护失败退避的内存状态;
// 重启清零无害,最多多打一次失败请求。
func (s *Server) setChallengeBackoff(vid string) {
	s.challengeMu.Lock()
	defer s.challengeMu.Unlock()
	if s.challengeBackoff == nil {
		s.challengeBackoff = map[string]time.Time{}
	}
	s.challengeBackoff[vid] = time.Now().Add(challengeJoinBackoff)
}

func (s *Server) challengeBackoffUntil(vid string) time.Time {
	s.challengeMu.Lock()
	defer s.challengeMu.Unlock()
	return s.challengeBackoff[vid]
}
