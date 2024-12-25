package weread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Refresh 用 refreshToken 换新 accessToken。
//
// 服务端可能同时轮换 refreshToken:返回值里的 RefreshToken 为空表示沿用旧的,
// 非空表示已轮换——调用方必须把返回的凭据立刻持久化,丢了轮换值就只能重新扫码。
func (c *Client) Refresh(ctx context.Context, creds *Credentials) (*Credentials, error) {
	if creds == nil || creds.RefreshToken == "" || creds.DeviceID == "" {
		return nil, errors.New("凭据不完整,无法刷新")
	}
	ts := unixMilli()
	random := randIntn(1000) + 1 // 参考实现的刷新签名 random 取 1..1000
	body, status, err := c.postJSON(ctx, BaseURL+"/login", versionHeaders(), map[string]any{
		"deviceId":     creds.DeviceID,
		"deviceName":   deviceName,
		"inBackground": 0,
		"kickType":     1,
		"random":       random,
		"refCgi":       "",
		"refreshToken": creds.RefreshToken,
		"signature":    sign(ts, creds.DeviceID, random),
		"timestamp":    ts,
		"trackId":      "",
		"deviceType":   deviceType,
	})
	if err != nil {
		return nil, fmt.Errorf("刷新请求失败: %w", err)
	}
	var resp loginResp
	_ = json.Unmarshal(body, &resp)
	if status != http.StatusOK || resp.AccessToken == "" {
		return nil, fmt.Errorf("刷新失败,refreshToken 可能已失效需要重新扫码: errCode=%s errMsg=%q (HTTP %d)",
			strings.TrimSpace(string(resp.ErrCode)), resp.ErrMsg, status)
	}
	next := &Credentials{
		Vid:          rawString(resp.Vid),
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		DeviceID:     creds.DeviceID,
	}
	if next.Vid == "" {
		next.Vid = creds.Vid
	}
	if next.Vid != creds.Vid {
		return nil, errors.New("刷新返回了另一个账号的凭据,拒绝写入")
	}
	if next.RefreshToken == "" {
		next.RefreshToken = creds.RefreshToken
	}
	return next, nil
}
