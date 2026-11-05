// 「阅读挑战」页 API:官方挑战赛的进度查询与报名。
package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/27Aaron/weread-kit/internal/weread"
)

// handleChallengeDetail 返回指定账号的官方挑战赛进度详情(version=v3,
// challengeList[0] 为进行中的那一场,未报名时为空数组)。
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
