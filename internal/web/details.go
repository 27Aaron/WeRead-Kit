package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"wxread/internal/store"
	"wxread/internal/weread"
)

// handleAccountDetails 聚合一个账号的用户信息、余额、会员卡和书架。
// 四路独立采集,单路失败不影响其它路;会话过期时自动续期并整体重试一次。
func (s *Server) handleAccountDetails(w http.ResponseWriter, r *http.Request) {
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

	result, expired, err := s.collectDetails(r.Context(), toWeread(c))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if expired {
		// 凭据过期:续期落库后重试一次。
		refreshed, rerr := s.client.Refresh(r.Context(), toWeread(c))
		if rerr != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("续期失败: %w", rerr))
			return
		}
		if serr := store.Save(s.db, toStore(alias, refreshed)); serr != nil {
			writeErr(w, http.StatusInternalServerError, serr)
			return
		}
		if c, err = store.Load(s.db, alias); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		result, expired, err = s.collectDetails(r.Context(), toWeread(c))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if expired {
			writeErr(w, http.StatusUnauthorized, errors.New("凭据已失效,请重新扫码登录"))
			return
		}
	}

    if raw, ok := result["user"].(json.RawMessage); ok {
        if err := saveProfile(s.db, c, raw); err != nil {
            errs, _ := result["errors"].(map[string]string)
            if errs == nil { errs = map[string]string{} }
            errs["profile"] = "保存用户资料失败"
            result["errors"] = errs
        }
    }
    result["alias"] = alias
	if c.Remark != "" {
		result["remark"] = c.Remark
	}
	writeJSON(w, http.StatusOK, result)
}

// collectDetails 采集四路数据。第二返回值为 true 表示中途发现会话过期,
// 调用方应续期后整体重试一次。
func (s *Server) collectDetails(ctx context.Context, creds *weread.Credentials) (map[string]any, bool, error) {
	result := map[string]any{"books": []any{}}
	errs := map[string]string{}

	// 书架:移动端接口,现有凭据直连。
	shelf, err := s.client.ShelfSync(ctx, creds)
	switch {
	case err == nil:
		result["books"] = shelf.Books
	case errors.Is(err, weread.ErrSessionExpired):
		return nil, true, nil
	default:
		errs["shelf"] = err.Error()
	}

	// 用户/余额/会员卡:网页版接口,先桥接网页会话。
	cookie, err := s.client.WebCookie(ctx, creds)
	switch {
	case err == nil:
		for name, fetch := range map[string]func() (json.RawMessage, error){
			"user":    func() (json.RawMessage, error) { return s.client.WebUserInfo(ctx, cookie, creds.Vid) },
			"balance": func() (json.RawMessage, error) { return s.client.WebBalance(ctx, cookie) },
			"card":    func() (json.RawMessage, error) { return s.client.WebMemberCard(ctx, cookie) },
		} {
			data, err := fetch()
			switch {
			case err == nil:
				result[name] = data
			case errors.Is(err, weread.ErrSessionExpired):
				return nil, true, nil
			default:
				errs[name] = err.Error()
			}
		}
	case errors.Is(err, weread.ErrSessionExpired):
		return nil, true, nil
	default:
		errs["session"] = err.Error()
	}

	if len(errs) > 0 {
		result["errors"] = errs
	}
	return result, false, nil
}
