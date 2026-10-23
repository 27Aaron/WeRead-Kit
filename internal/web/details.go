package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// handleAccountDetails 返回账号详情(用户信息/会员卡/书架)。
//
// GET 走数据库缓存,毫秒级响应;POST 强制回源微信读书并更新缓存,
// 前端「刷新数据」按钮走 POST。缓存不存在时 GET 也会回源并落库。
func (s *Server) handleAccountDetails(w http.ResponseWriter, r *http.Request) {
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

	if r.Method != http.MethodPost && c.Profile != "" && c.Card != "" && c.Shelf != "" {
		resp := map[string]any{
			"vid":       vid,
			"remark":    c.Remark,
			"user":      json.RawMessage(c.Profile),
			"card":      json.RawMessage(c.Card),
			"books":     json.RawMessage(c.Shelf),
			"cached_at": c.DetailsCachedAt,
			"running":   s.farmRunning(vid),
		}
		if cfg, rerr := store.GetReadingConfig(s.db, vid); rerr == nil {
			resp["reading"] = cfg
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// 採集途中的續期已由 OnRefresh 落庫。
	d, err := s.client.Details(r.Context(), toWeread(c))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	persistDetails(s.db, vid, d)

	writeJSON(w, http.StatusOK, map[string]any{
		"vid":       vid,
		"remark":    c.Remark,
		"user":      d.User,
		"card":      d.Card,
		"books":     d.Shelf,
		"errors":    d.Errs,
		"cached_at": time.Now().Unix(),
		"reading":   mustReadingConfig(s.db, vid),
		"running":   s.farmRunning(vid),
	})
}

// persistDetails 把一次採集結果寫入詳情快取與資料快取;失敗項記入 d.Errs。
// 快取寫入失敗不影響本次回傳,前端下次仍可強制刷新。
func persistDetails(db *sql.DB, vid string, d *weread.Details) {
	var shelfJSON []byte
	if _, failed := d.Errs["shelf"]; !failed {
		// 書架拉取失敗時傳空,保留舊快取而不是寫入空書架。
		shelfJSON, _ = json.Marshal(d.Shelf)
	}
	if err := store.SaveDetailsCache(db, vid, d.User, d.Card, shelfJSON); err != nil {
		d.Errs["cache"] = "缓存写入失败: " + err.Error()
	}
	// 同步顯示名稱/頭像快取(獨立於詳情快取,列表頁用)。
	if len(d.User) > 0 {
		if err := saveProfile(db, vid, d.User); err != nil {
			d.Errs["profile"] = "保存用户资料失败"
		}
	}
}

func mustReadingConfig(db *sql.DB, vid string) *store.ReadingConfig {
	cfg, err := store.GetReadingConfig(db, vid)
	if err != nil {
		return store.DefaultReadingConfig(vid)
	}
	return cfg
}
