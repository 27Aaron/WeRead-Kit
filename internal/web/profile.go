package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

func saveProfile(db *sql.DB, c *store.Credential, raw json.RawMessage) error {
	var user struct {
		User     json.RawMessage `json:"user"`
		Name     string          `json:"name"`
		Nickname string          `json:"nickname"`
		Avatar   string          `json:"avatar"`
		UserVid  json.Number     `json:"userVid"`
	}
	if err := json.Unmarshal(raw, &user); err != nil {
		return err
	}
	if len(user.User) > 0 && string(user.User) != "null" {
		nested := user.User
		user.User = nil
		if err := json.Unmarshal(nested, &user); err != nil {
			return err
		}
	}
	if user.Name == "" {
		user.Name = user.Nickname
	}
	if user.Name == "" {
		return errors.New("用户资料缺少昵称")
	}
	vid := user.UserVid.String()
	if vid == "" {
		vid = c.Vid
	}
	if vid != c.Vid {
		return fmt.Errorf("用户资料 ID 与账号不一致")
	}
	return store.UpdateProfile(db, vid, user.Name, user.Avatar, vid)
}

func syncProfile(ctx context.Context, db *sql.DB, client *weread.Client, c *store.Credential) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	creds := toWeread(c)
	bridge := func() (string, error) {
		cookie, err := client.WebCookie(ctx, creds)
		if err != nil {
			return "", err
		}
		return cookie, nil
	}
	refresh := func() error {
		next, err := client.Refresh(ctx, creds)
		if err != nil {
			return err
		}
		if err := store.Save(db, toStore(c.Vid, next)); err != nil {
			return err
		}
		creds = next
		c.AccessToken = next.AccessToken
		c.RefreshToken = next.RefreshToken
		c.DeviceID = next.DeviceID
		return nil
	}
	cookie, err := bridge()
	if errors.Is(err, weread.ErrSessionExpired) {
		if err = refresh(); err == nil {
			cookie, err = bridge()
		}
	}
	if err != nil {
		return err
	}
	raw, err := client.WebUserInfo(ctx, cookie, c.Vid)
	if errors.Is(err, weread.ErrSessionExpired) {
		if err = refresh(); err == nil {
			cookie, err = bridge()
			if err == nil {
				raw, err = client.WebUserInfo(ctx, cookie, c.Vid)
			}
		}
	}
	if err != nil {
		return err
	}
	return saveProfile(db, c, raw)
}

// 既有账号的资料由独立流程补充,账号列表加载不等待微信读书接口。
func (s *Server) handleSyncProfile(w http.ResponseWriter, r *http.Request) {
	c, err := store.Load(s.db, r.PathValue("vid"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if c.ProfileUpdatedAt == 0 {
		if err := syncProfile(r.Context(), s.db, s.client, c); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		c, err = store.Load(s.db, c.Vid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, toView(c))
}
