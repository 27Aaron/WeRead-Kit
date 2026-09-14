package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
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
		}
		if cfg, rerr := store.GetReadingConfig(s.db, vid); rerr == nil {
			resp["reading"] = cfg
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	d, next, err := s.client.Details(r.Context(), toWeread(c))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// 采集途中发生过续期,轮换出的新凭据必须落库。
	if next != nil {
		if serr := store.Save(s.db, toStore(vid, next)); serr != nil {
			writeErr(w, http.StatusInternalServerError, serr)
			return
		}
		if c, err = store.Load(s.db, vid); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}

	shelfJSON, merr := json.Marshal(d.Shelf)
	if merr != nil {
		writeErr(w, http.StatusInternalServerError, merr)
		return
	}
	if cerr := store.SaveDetailsCache(s.db, vid, d.User, d.Card, shelfJSON); cerr != nil {
		// 缓存写失败不影响本次返回,前端下次仍可强制刷新。
		if d.Errs == nil {
			d.Errs = map[string]string{}
		}
		d.Errs["cache"] = "缓存写入失败: " + cerr.Error()
	}
	// 同步展示名/头像缓存(独立于详情缓存,列表页用)。
	if err := saveProfile(s.db, c, d.User); err != nil {
		if d.Errs == nil {
			d.Errs = map[string]string{}
		}
		d.Errs["profile"] = "保存用户资料失败"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vid":       vid,
		"remark":    c.Remark,
		"user":      d.User,
		"card":      d.Card,
		"books":     d.Shelf,
		"errors":    d.Errs,
		"cached_at": time.Now().Unix(),
		"reading":   mustReadingConfig(s.db, vid),
	})
}

func mustReadingConfig(db *sql.DB, vid string) *store.ReadingConfig {
	cfg, err := store.GetReadingConfig(db, vid)
	if err != nil {
		return store.DefaultReadingConfig(vid)
	}
	return cfg
}
