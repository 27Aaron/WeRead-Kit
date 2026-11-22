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
	// refreshTimeout 是单次刷新请求的上限,独立于调用方 ctx。
	refreshTimeout = 20 * time.Second
)

// Refresh 用 refreshToken 换新 accessToken。
//
// 服务端可能同时轮换 refreshToken:返回值里的 RefreshToken 为空表示沿用旧的,
// 非空表示已轮换。调用方必须立刻持久化返回的凭据,丢了轮换值就只能重新扫码。
func (c *Client) Refresh(ctx context.Context, creds *Credentials) (*Credentials, error) {
	if creds == nil || creds.RefreshToken == "" || creds.DeviceID == "" {
		return nil, errors.New("凭据不完整,无法刷新")
	}

	// 同一账号同一时刻只允许一次刷新。微信读书可能轮换 refreshToken,
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
				// 上一次刷新已成功结束:调用方手里的凭据若不是最新一代,其
				// refreshToken 很可能已被这条链路消费过,绝不能拿它再打 /login,
				// 直接交还最新一代(进程内所有轮换都经此 client 并已落库)。
				// 手里就是最新一代说明是正常的到期续期,走真实刷新。
				if call.err == nil && !sameCredentials(call.next, source) {
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

		// 请求送出后服务端就可能已轮换 refreshToken:调用方中途取消(如浏览器断开)
		// 也要等到结果并交给 OnRefresh 落库,否则新 Token 遗失只能重新扫码。
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
