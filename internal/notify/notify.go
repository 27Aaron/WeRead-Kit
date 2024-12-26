// Package notify 实现推送渠道的发送:Bark、Telegram、Server酱、pushplus。
// 各渠道均为一次 HTTP 调用,无 SDK 依赖。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 支持的渠道类型标识。
const (
	TypeBark      = "bark"
	TypeTelegram  = "telegram"
	TypeServerChan = "serverchan"
	TypePushPlus  = "pushplus"
)

// RequiredParams 返回各渠道必填的参数名,用于保存前校验。
func RequiredParams(chType string) []string {
	switch chType {
	case TypeBark:
		return []string{"device_key"}
	case TypeTelegram:
		return []string{"bot_token", "chat_id"}
	case TypeServerChan:
		return []string{"send_key"}
	case TypePushPlus:
		return []string{"token"}
	}
	return nil
}

// ValidType 判断渠道类型是否受支持。
func ValidType(chType string) bool {
	return len(RequiredParams(chType)) > 0
}

// DefaultParams 返回渠道参数的默认值(Bark 的自建服务端地址),其余渠道无默认项。
func DefaultParams(chType string) map[string]string {
	if chType == TypeBark {
		return map[string]string{"server": "https://api.day.app"}
	}
	return map[string]string{}
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// postJSON 发送 JSON 请求并解析响应。
func postJSON(ctx context.Context, rawURL string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json; charset=UTF-8")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

func postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

// Send 向指定渠道推送一条消息。
func Send(ctx context.Context, chType string, params map[string]string, title, body string) error {
	switch chType {
	case TypeBark:
		return sendBark(ctx, params, title, body)
	case TypeTelegram:
		return sendTelegram(ctx, params, title, body)
	case TypeServerChan:
		return sendServerChan(ctx, params, title, body)
	case TypePushPlus:
		return sendPushPlus(ctx, params, title, body)
	}
	return fmt.Errorf("未知推送类型 %q", chType)
}

// Bark:POST {server}/{device_key},自建服务端可通过 server 参数覆盖。
func sendBark(ctx context.Context, p map[string]string, title, body string) error {
	key := strings.TrimSpace(p["device_key"])
	if key == "" {
		return fmt.Errorf("缺少 device_key")
	}
	server := strings.TrimRight(strings.TrimSpace(p["server"]), "/")
	if server == "" {
		server = "https://api.day.app"
	}
	data, err := postJSON(ctx, server+"/"+url.PathEscape(key), map[string]string{
		"title": title,
		"body":  body,
		"group": "wxread",
	})
	if err != nil {
		return err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &resp); err == nil && resp.Code != 200 {
		return fmt.Errorf("bark 返回错误: %s", resp.Message)
	}
	return nil
}

// Telegram:Bot API sendMessage。
func sendTelegram(ctx context.Context, p map[string]string, title, body string) error {
	token := strings.TrimSpace(p["bot_token"])
	chatID := strings.TrimSpace(p["chat_id"])
	if token == "" || chatID == "" {
		return fmt.Errorf("缺少 bot_token 或 chat_id")
	}
	rawURL := "https://api.telegram.org/bot" + token + "/sendMessage"
	data, err := postJSON(ctx, rawURL, map[string]string{
		"chat_id": chatID,
		"text":    title + "\n" + body,
	})
	if err != nil {
		return err
	}
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &resp); err == nil && !resp.OK {
		return fmt.Errorf("telegram 返回错误: %s", resp.Description)
	}
	return nil
}

// Server酱 Turbo:POST {sendkey}.send,消息经微信服务号送达。
func sendServerChan(ctx context.Context, p map[string]string, title, body string) error {
	key := strings.TrimSpace(p["send_key"])
	if key == "" {
		return fmt.Errorf("缺少 send_key")
	}
	form := url.Values{}
	form.Set("title", title)
	form.Set("desp", body)
	data, err := postForm(ctx, "https://sctapi.ftqq.com/"+url.PathEscape(key)+".send", form)
	if err != nil {
		return err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &resp); err == nil && resp.Code != 0 {
		return fmt.Errorf("serverchan 返回错误: %s", resp.Message)
	}
	return nil
}

// pushplus:POST /send,template 用 txt 避免内容被当作 HTML 处理。
func sendPushPlus(ctx context.Context, p map[string]string, title, body string) error {
	token := strings.TrimSpace(p["token"])
	if token == "" {
		return fmt.Errorf("缺少 token")
	}
	data, err := postJSON(ctx, "https://www.pushplus.plus/send", map[string]string{
		"token":    token,
		"title":    title,
		"content":  body,
		"template": "txt",
	})
	if err != nil {
		return err
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &resp); err == nil && resp.Code != 200 {
		return fmt.Errorf("pushplus 返回错误: %s", resp.Msg)
	}
	return nil
}
