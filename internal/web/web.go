// Package web 提供账号管理与阅读任务 API。
package web

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"wxread/internal/store"
	"wxread/internal/weread"
)

//go:embed static
var staticFiles embed.FS

// Server 是 Web UI 的 HTTP 服务。
type Server struct {
	db     *sql.DB
	client *weread.Client
	logins *loginManager
}

func New(db *sql.DB) *Server {
	client := weread.NewClient()
	return &Server{db: db, client: client, logins: newLoginManager(db, client)}
}

func (s *Server) Handler() http.Handler {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil { panic("web: embedded static missing: " + err.Error()) }
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(sub))
	mux.HandleFunc("GET /api/accounts", s.handleListAccounts)
	mux.HandleFunc("DELETE /api/accounts/{alias}", s.handleDeleteAccount)
	mux.HandleFunc("PUT /api/accounts/{alias}/remark", s.handleUpdateRemark)
	mux.HandleFunc("POST /api/accounts/{alias}/refresh", s.handleRefreshAccount)
	mux.HandleFunc("POST /api/accounts/{alias}/profile", s.handleSyncProfile)
	mux.HandleFunc("GET /api/accounts/{alias}/details", s.handleAccountDetails)
	mux.HandleFunc("POST /api/accounts/{alias}/details", s.handleAccountDetails)
	mux.HandleFunc("GET /api/accounts/{alias}/reading", s.handleReadingConfig)
	mux.HandleFunc("POST /api/accounts/{alias}/reading", s.handleReadingConfig)
	mux.HandleFunc("POST /api/accounts/{alias}/reading/run", s.handleReadingRun)
	mux.HandleFunc("POST /api/accounts/{alias}/reading/pause", s.handleReadingPause)
	mux.HandleFunc("POST /api/accounts/{alias}/reading/stop", s.handleReadingStop)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("DELETE /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/settings/push", s.handlePushChannels)
	mux.HandleFunc("POST /api/settings/push", s.handlePushChannels)
	mux.HandleFunc("POST /api/settings/push/{type}/test", s.handlePushTest)
	mux.HandleFunc("GET /api/accounts/{alias}/token", s.handleAccountToken)
	mux.HandleFunc("POST /api/login", s.handleStartLogin)
	mux.HandleFunc("GET /api/login/{id}", s.handleLoginStatus)
	mux.HandleFunc("GET /api/login/{id}/qr.png", s.handleLoginQR)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type accountView struct {
	Name             string `json:"name"`
	Avatar           string `json:"avatar"`
	UserVid          string `json:"user_vid"`
	ProfileUpdatedAt int64  `json:"profile_updated_at"`
	Alias            string `json:"alias"`
	Remark           string `json:"remark"`
	Vid              string `json:"vid"`
	DeviceID         string `json:"device_id"`
	HasAccessToken   bool   `json:"has_access_token"`
	RotatedAt        int64  `json:"rotated_at"`
	CreatedAt        int64  `json:"created_at"`
}

func toView(c *store.Credential) accountView {
	return accountView{
		Name: c.Name, Avatar: c.Avatar, UserVid: c.UserVid, ProfileUpdatedAt: c.ProfileUpdatedAt,
		Alias:          c.Alias,
		Remark:         c.Remark,
		Vid:            c.Vid,
		DeviceID:       c.DeviceID,
		HasAccessToken: c.AccessToken != "",
		RotatedAt:      c.RotatedAt.Unix(),
		CreatedAt:      c.CreatedAt.Unix(),
	}
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := store.List(s.db)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]accountView, 0, len(list))
	for _, c := range list {
		views = append(views, toView(c))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	err := store.Delete(s.db, r.PathValue("alias"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateRemark(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Remark string `json:"remark"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
		return
	}
	if len([]rune(body.Remark)) > maxRemarkRunes {
		writeErr(w, http.StatusBadRequest, errors.New("备注过长(最多 100 字)"))
		return
	}
	err := store.UpdateRemark(s.db, r.PathValue("alias"), body.Remark)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
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
	refreshed, err := s.client.Refresh(r.Context(), toWeread(c))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := store.Save(s.db, toStore(alias, refreshed)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	updated, err := store.Load(s.db, alias)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, toView(updated))
}

func (s *Server) handleAccountToken(w http.ResponseWriter, r *http.Request) {
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
	maxAge := 24 * time.Hour
	if v := r.URL.Query().Get("max_age"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			maxAge = parsed
		}
	}
	if r.URL.Query().Get("force") == "1" || time.Since(c.RotatedAt) > maxAge {
		refreshed, err := s.client.Refresh(r.Context(), toWeread(c))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		if err := store.Save(s.db, toStore(alias, refreshed)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if c, err = store.Load(s.db, alias); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": c.AccessToken,
		"vid":          c.Vid,
		"rotated_at":   c.RotatedAt.Unix(),
	})
}

// ---- 扫码登录会话 ----

const maxRemarkRunes = 100

type startLoginBody struct {
	Remark string `json:"remark"`
}

func (s *Server) handleStartLogin(w http.ResponseWriter, r *http.Request) {
	var body startLoginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
		return
	}
	remark := strings.TrimSpace(body.Remark)
	if len([]rune(remark)) > maxRemarkRunes {
		writeErr(w, http.StatusBadRequest, errors.New("备注过长(最多 100 字)"))
		return
	}
	id, err := s.logins.start(remark)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// 等二维码就绪再返回:前端拿到 qr_url 时 PNG 必定存在,
	// 票据阶段的即时失败也在这里直接报给用户。
	if err := s.logins.waitQR(r.Context(), id, 8*time.Second); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id":     id,
		"qr_url": "/api/login/" + id + "/qr.png",
	})
}

func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.logins.get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("登录会话不存在或已过期"))
		return
	}
	resp := map[string]any{"status": sess.status}
	if sess.errMsg != "" {
		resp["error"] = sess.errMsg
	}
	if sess.status == "success" {
		resp["account"] = map[string]string{
			"alias":  sess.alias,
			"vid":    sess.creds.Vid,
			"remark": sess.remark,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLoginQR(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.logins.get(r.PathValue("id"))
	if !ok || sess.confirmURL == "" {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(sess.confirmURL, qrcode.Medium, 360)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("content-type", "image/png")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(png)
}

func toWeread(c *store.Credential) *weread.Credentials {
	return &weread.Credentials{
		Vid:          c.Vid,
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		DeviceID:     c.DeviceID,
	}
}

func toStore(alias string, c *weread.Credentials) *store.Credential {
	return &store.Credential{
		Alias:        alias,
		Vid:          c.Vid,
		RefreshToken: c.RefreshToken,
		DeviceID:     c.DeviceID,
		AccessToken:  c.AccessToken,
	}
}
