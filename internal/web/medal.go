// 「我的阅读」页 API:勋章墙。
package web

import (
	"encoding/json"
	"net/http"

	"github.com/27Aaron/weread-kit/internal/weread"
)

// handleMedalList 返回指定账号的勋章列表(已获得/未解锁/佩戴中)。
// GET /api/accounts/{vid}/medals
func (s *Server) handleMedalList(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	data, _, err := s.weeklyCall(r.Context(), vid, func(creds *weread.Credentials) (json.RawMessage, error) {
		return s.client.MedalList(r.Context(), creds)
	})
	if err != nil {
		writeWeeklyErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vid":    vid,
		"medals": json.RawMessage(data),
	})
}
