package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/27Aaron/weread-kit/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const webSessionTTL = 7 * 24 * time.Hour

// sessionKeySetting 是会话签名密钥在 weread_setting 表中的键名。
// 密钥跨重启持久,进程重启不再使已登录浏览器失效;
// 与凭据同库存放,信任域一致,换库文件导致全部会话失效属预期。
const sessionKeySetting = "web_session_key"

// loadOrCreateSessionKey 取回持久化的会话签名密钥;首次运行时生成并落库。
func loadOrCreateSessionKey(db *sql.DB) (string, error) {
	key, err := store.GetSetting(db, sessionKeySetting)
	if !errors.Is(err, store.ErrNotFound) {
		return key, err
	}
	if key, err = newSessionID(); err != nil {
		return "", err
	}
	return key, store.SetSetting(db, sessionKeySetting, key)
}

func (s *Server) sessionToken(expires int64) string {
	stamp := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(stamp))
	return stamp + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(token string) bool {
	stamp, _, ok := strings.Cut(token, ".")
	expires, err := strconv.ParseInt(stamp, 10, 64)
	return ok && err == nil && expires > time.Now().Unix() && hmac.Equal([]byte(token), []byte(s.sessionToken(expires)))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/favicon.png" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || (s.username == "" && s.password == "") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("weread_session")
		if err != nil || !s.validSession(c.Value) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeErr(w, http.StatusUnauthorized, errors.New("登录已过期，请重新登录"))
			} else {
				http.Redirect(w, r, "/login", http.StatusFound)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() == nil && s.username != "" && s.password != "" &&
		subtle.ConstantTimeCompare([]byte(r.Form.Get("username")), []byte(s.username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(r.Form.Get("password")), []byte(s.password)) == 1 {
		expires := time.Now().Add(webSessionTTL)
		http.SetCookie(w, &http.Cookie{Name: "weread_session", Value: s.sessionToken(expires.Unix()), Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(webSessionTTL.Seconds())})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	store.AddLog(s.db, "warn", "auth", "", "Web 登录失败")
	http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
}
