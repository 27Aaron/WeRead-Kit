package weread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// refreshReuseWindow 內重複提交同一份舊憑據的刷新請求直接沿用上次結果。
	refreshReuseWindow = 5 * time.Minute
	// refreshTimeout 是單次刷新請求的上限,獨立於呼叫方 ctx。
	refreshTimeout = 20 * time.Second
)

// Refresh 用 refreshToken 换新 accessToken。
//
// 服务端可能同时轮换 refreshToken:返回值里的 RefreshToken 为空表示沿用旧的,
// 非空表示已轮换——调用方必须把返回的凭据立刻持久化,丢了轮换值就只能重新扫码。
func (c *Client) Refresh(ctx context.Context, creds *Credentials) (*Credentials, error) {
	if creds == nil || creds.RefreshToken == "" || creds.DeviceID == "" {
		return nil, errors.New("凭据不完整,无法刷新")
	}

	// 同一帳號同一時刻只允許一次刷新。微信讀書可能輪換 refreshToken,
	// 重复消费旧 Token 会让后一个请求覆盖前一个请求刚保存的新凭据。
	source := cloneCredentials(creds)
	for {
		c.refreshMu.Lock()
		if c.refreshes == nil {
			c.refreshes = make(map[string]*refreshCall)
		}
		call := c.refreshes[creds.Vid]
		if call != nil {
			select {
			case <-call.done:
				// 上一次刷新已結束:持有同一份舊憑據的呼叫在沿用窗口內直接拿結果,
				// 不再用已被輪換掉的 refreshToken 重複請求。
				if call.err == nil && sameCredentials(call.source, source) && time.Since(call.finished) < refreshReuseWindow {
					c.refreshMu.Unlock()
					return cloneCredentials(call.next), nil
				}
				call = nil
			default:
			}
		}
		if call != nil {
			c.refreshMu.Unlock()
			select {
			case <-call.done:
				if sameCredentials(call.source, source) {
					return cloneCredentials(call.next), call.err
				}
				// 当前凭据已不同,说明前一次刷新可能已经完成轮换;
				// 等它退出后重新尝试,确保同一账号不会并发刷新。
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		call = &refreshCall{source: source, done: make(chan struct{})}
		c.refreshes[creds.Vid] = call
		c.refreshMu.Unlock()

		// 請求送出後服務端就可能已輪換 refreshToken:呼叫方中途取消(如瀏覽器斷開)
		// 也要等到結果並交給 OnRefresh 落庫,否則新 Token 遺失只能重新掃碼。
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		next, err := c.refresh(rctx, creds)
		cancel()
		if err == nil && c.OnRefresh != nil {
			c.OnRefresh(cloneCredentials(next))
		}
		c.refreshMu.Lock()
		call.next, call.err, call.finished = cloneCredentials(next), err, time.Now()
		close(call.done)
		c.refreshMu.Unlock()
		return next, err
	}
}

func (c *Client) refresh(ctx context.Context, creds *Credentials) (*Credentials, error) {
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

func cloneCredentials(creds *Credentials) *Credentials {
	if creds == nil {
		return nil
	}
	copy := *creds
	return &copy
}

func sameCredentials(a, b *Credentials) bool {
	return a != nil && b != nil && a.Vid == b.Vid &&
		a.AccessToken == b.AccessToken && a.RefreshToken == b.RefreshToken && a.DeviceID == b.DeviceID
}
