// 推送设置的 API 层:四类渠道固定平铺,按类型读写配置、发送测试、广播会话结果。
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/27Aaron/weread-kit/internal/notify"
	"github.com/27Aaron/weread-kit/internal/store"
)

var pushTypes = []string{
	notify.TypeBark,
	notify.TypeTelegram,
	notify.TypeServerChan,
	notify.TypePushPlus,
}

type pushSaveBody struct {
	Type    string            `json:"type"`
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params"`
}

// handlePushChannels GET 返回四类渠道的完整配置(未保存过的填默认值);
// POST 按类型保存配置,启用时校验必填参数。
func (s *Server) handlePushChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handlePushSave(w, r)
		return
	}
	stored := map[string]*store.PushChannel{}
	rows, err := store.ListPushChannels(s.db)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, ch := range rows {
		stored[ch.Type] = ch
	}
	out := []map[string]any{}
	for _, chType := range pushTypes {
		enabled, params := false, notify.DefaultParams(chType)
		if ch, ok := stored[chType]; ok {
			enabled = ch.Enabled
			var saved map[string]string
			if json.Unmarshal([]byte(ch.Params), &saved) == nil {
				for k, v := range saved {
					params[k] = v
				}
			}
		}
		out = append(out, map[string]any{
			"type": chType, "enabled": enabled, "params": params,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePushSave(w http.ResponseWriter, r *http.Request) {
	var body pushSaveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求体不是 JSON"))
		return
	}
	if !notify.ValidType(body.Type) {
		writeErr(w, http.StatusBadRequest, errors.New("不支持的推送渠道类型"))
		return
	}
	params := notify.DefaultParams(body.Type)
	for key, value := range body.Params {
		params[key] = strings.TrimSpace(value)
	}
	// 只在启用时要求必填参数:停用状态下允许先留着半成品配置。
	if body.Enabled {
		for _, key := range notify.RequiredParams(body.Type) {
			if params[key] == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("启用前请先填写 %s", key))
				return
			}
		}
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := store.SavePushChannel(s.db, &store.PushChannel{
		Type:    body.Type,
		Enabled: body.Enabled,
		Params:  string(paramsJSON),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"type": body.Type, "enabled": body.Enabled, "params": params,
	})
}

// handlePushTest 向指定渠道发送一条测试消息,验证参数是否可用。
// 请求体可携带 params(界面当前填写的值),未保存的修改也能即时验证。
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	chType := r.PathValue("type")
	if !notify.ValidType(chType) {
		writeErr(w, http.StatusBadRequest, errors.New("不支持的推送渠道类型"))
		return
	}
	params := notify.DefaultParams(chType)
	if ch, err := store.GetPushChannel(s.db, chType); err == nil {
		var saved map[string]string
		if json.Unmarshal([]byte(ch.Params), &saved) == nil {
			for k, v := range saved {
				params[k] = v
			}
		}
	}
	var body struct {
		Params map[string]string `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	for key, value := range body.Params {
		params[key] = value
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := notify.Send(ctx, chType, params, "weread-kit 测试通知", "收到这条消息说明推送配置有效。"); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// notifyFarmResult 向所有启用的渠道广播阅读会话结果。尽力而为:单渠道失败只记日志。
func (s *Server) notifyFarmResult(vid, level, title, body string) {
	channels, err := store.ListEnabledPushChannels(s.db)
	if err != nil || len(channels) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	emblem := "✅ "
	if level != "info" {
		emblem = "⚠️ "
	}
	for _, ch := range channels {
		var params map[string]string
		if err := json.Unmarshal([]byte(ch.Params), &params); err != nil {
			continue
		}
		if err := notify.Send(ctx, ch.Type, params, emblem+title, body); err != nil {
			s.logf("warn", "push", vid, "%s 渠道推送失败: %v", ch.Type, err)
		} else {
			s.logf("info", "push", vid, "%s 渠道推送成功: %s", ch.Type, title)
		}
	}
}

// readingBookTitle 从书架缓存解析书名,解析不到时退回 bookID。
func (s *Server) readingBookTitle(vid, bookID string) string {
	c, err := store.Load(s.db, vid)
	if err != nil {
		return bookID
	}
	var shelf []struct {
		BookID string `json:"bookId"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(c.Shelf), &shelf); err != nil {
		return bookID
	}
	for _, b := range shelf {
		if b.BookID == bookID && b.Title != "" {
			return b.Title
		}
	}
	return bookID
}
