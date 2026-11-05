// 「我的阅读」页 API:阅读成就(里程碑/书币余额/每日阅读时长)聚合查询。
package web

import (
	"encoding/json"
	"net/http"

	"github.com/27Aaron/weread-kit/internal/weread"
)

// handleReadingStats 聚合返回里程碑、书币余额与每日阅读时长。
// 三项独立拉取,单项失败不影响其余(对应字段为 null),与详情缓存同一容错思路。
// GET /api/accounts/{vid}/stats
func (s *Server) handleReadingStats(w http.ResponseWriter, r *http.Request) {
	vid := r.PathValue("vid")
	fetch := func(op func(*weread.Credentials) (json.RawMessage, error)) any {
		data, _, err := s.weeklyCall(r.Context(), vid, op)
		if err != nil {
			return nil
		}
		return json.RawMessage(data)
	}
	resp := map[string]any{
		"vid":      vid,
		"milestone": fetch(func(creds *weread.Credentials) (json.RawMessage, error) {
			return s.client.ReadMilestone(r.Context(), creds)
		}),
		"balance": fetch(func(creds *weread.Credentials) (json.RawMessage, error) {
			return s.client.PayBalance(r.Context(), creds)
		}),
		"summary": fetch(func(creds *weread.Credentials) (json.RawMessage, error) {
			return s.client.ReadDataSummary(r.Context(), creds)
		}),
	}
	writeJSON(w, http.StatusOK, resp)
}
