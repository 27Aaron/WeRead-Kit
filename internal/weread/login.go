package weread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// loginOverall 是整个扫码流程的总时限,与参考实现一致(5 分钟)。
const (
	loginOverall  = 5 * time.Minute
	pollPerCall   = 65 * time.Second
	pollIdleDelay = time.Second
)

// 扫码流程的可识别失败,调用方(如 Web 层)据此向用户展示对应提示。
var (
	ErrQRExpired  = errors.New("二维码已过期,请重新扫码")
	ErrQRDeclined = errors.New("你在微信中拒绝了授权")
)

// Login 执行完整扫码流程:取票据 → 生成二维码 → 轮询扫码状态 → 用 code 换凭据。
//
// onQR 收到待扫描的确认链接(由调用方渲染成终端二维码);
// onStatus 收到"已扫码"等进度提示;两个回调都可为 nil。
// deviceID 传空则生成新设备 ID;复用旧值可保持同一设备身份。
func (c *Client) Login(ctx context.Context, deviceID string, onQR func(confirmURL string), onStatus func(status string)) (*Credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, loginOverall)
	defer cancel()
	if deviceID == "" {
		deviceID = NewDeviceID()
	}
	ticket, err := c.wxticket(ctx)
	if err != nil {
		return nil, err
	}
	qrUUID, err := c.qrConnect(ctx, ticket)
	if err != nil {
		return nil, err
	}
	confirmURL := "https://open.weixin.qq.com/connect/confirm?uuid=" + url.QueryEscape(qrUUID)
	if onQR != nil {
		onQR(confirmURL)
	}
	wxCode, err := c.pollForCode(ctx, qrUUID, onStatus)
	if err != nil {
		return nil, err
	}
	return c.exchange(ctx, wxCode, deviceID)
}

type wxTicket struct {
	Signature string
	TimeStamp string
}

func (c *Client) wxticket(ctx context.Context) (*wxTicket, error) {
	body, status, err := c.getJSON(ctx, BaseURL+"/wxticket?nonceStr="+nonceStr, versionHeaders())
	if err != nil {
		return nil, fmt.Errorf("获取 wxticket 失败: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("获取 wxticket 失败: HTTP %d", status)
	}
	var raw struct {
		Signature string          `json:"signature"`
		TimeStamp json.RawMessage `json:"timeStamp"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("wxticket 响应不是 JSON: %w", err)
	}
	ts := strings.Trim(strings.TrimSpace(string(raw.TimeStamp)), `"`)
	if raw.Signature == "" || ts == "" {
		return nil, fmt.Errorf("wxticket 响应缺少签名或时间戳")
	}
	return &wxTicket{Signature: raw.Signature, TimeStamp: ts}, nil
}

func (c *Client) qrConnect(ctx context.Context, ticket *wxTicket) (string, error) {
	q := url.Values{}
	q.Set("appid", wxAppID)
	q.Set("noncestr", nonceStr)
	q.Set("timestamp", ticket.TimeStamp)
	q.Set("scope", wxScope)
	q.Set("signature", ticket.Signature)
	// 这一步打到微信开放平台,参考实现只带 UA,不带其他版本头。
	body, status, err := c.getJSON(ctx,
		"https://open.weixin.qq.com/connect/sdk/qrconnect?"+q.Encode(),
		map[string]string{"User-Agent": userAgent})
	if err != nil {
		return "", fmt.Errorf("请求微信 qrconnect 失败: %w", err)
	}
	var resp struct {
		ErrCode json.Number `json:"errcode"`
		UUID    string      `json:"uuid"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("qrconnect 响应不是 JSON: %w", err)
	}
	if status != http.StatusOK || resp.ErrCode.String() != "0" || resp.UUID == "" {
		return "", fmt.Errorf("qrconnect 失败: HTTP %d errcode=%s", status, resp.ErrCode.String())
	}
	return resp.UUID, nil
}

// pollForCode 长轮询扫码状态直到用户在微信中确认。
// 返回值是微信下发的 wx_code,用于换取微信读书凭据。
//
// wx_errcode 语义:408=无变化(服务端保持连接) 404=已扫码 405=已确认
// 402=二维码过期 403=用户拒绝。
func (c *Client) pollForCode(ctx context.Context, uuid string, onStatus func(string)) (string, error) {
	deadline := time.Now().Add(loginOverall)
	last := -1
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", errors.New("登录超时（5 分钟），请重新生成二维码")
		}

		q := url.Values{}
		q.Set("f", "json")
		q.Set("uuid", uuid)
		if last >= 0 {
			q.Set("last", fmt.Sprint(last))
		}
		callCtx, cancel := context.WithTimeout(ctx, min(pollPerCall, remaining))
		body, status, err := c.getJSON(callCtx,
			"https://long.open.weixin.qq.com/connect/l/qrconnect?"+q.Encode(),
			map[string]string{"User-Agent": "Mozilla/5.0"})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// 单次长轮询的网络抖动不致命,总时限内继续重试。
			if sleepCtx(ctx, pollIdleDelay) != nil {
				return "", ctx.Err()
			}
			continue
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("扫码轮询失败: HTTP %d", status)
		}

		var resp struct {
			WxErrCode int    `json:"wx_errcode"`
			WxCode    string `json:"wx_code"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", fmt.Errorf("扫码轮询响应不是 JSON: %w", err)
		}

		switch resp.WxErrCode {
		case 405:
			if resp.WxCode == "" {
				return "", errors.New("扫码已确认但服务端未返回 code")
			}
			return resp.WxCode, nil
		case 404:
			if onStatus != nil {
				onStatus("已扫码,请在微信中确认登录")
			}
		case 402:
			return "", ErrQRExpired
		case 403:
			return "", ErrQRDeclined
		case 408:
			// 服务端长轮询保持中,稍候再问。
			if err := sleepCtx(ctx, pollIdleDelay); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("扫码轮询返回未知状态 %d", resp.WxErrCode)
		}
		last = resp.WxErrCode
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// exchange 用 wx_code 换取凭据。对应参考实现 qrlogin.ts 的 exchange()。
func (c *Client) exchange(ctx context.Context, wxCode, deviceID string) (*Credentials, error) {
	ts := unixMilli()
	random := randIntn(1000) // 参考实现取 0..999
	creds, err := c.postLogin(ctx, map[string]any{
		"appFirstInstall": 1,
		"code":            wxCode,
		"deviceId":        deviceID,
		"deviceName":      deviceName,
		"installId":       newInstallID(),
		"isAutoLogout":    0,
		"isFromQrcode":    1,
		"random":          random,
		"signature":       sign(ts, deviceID, random),
		"timestamp":       ts,
		"trackId":         "",
		"deviceType":      deviceType,
	})
	if err != nil {
		return nil, err
	}
	creds.DeviceID = deviceID
	return creds, nil
}

type loginResp struct {
	Vid          json.RawMessage `json:"vid"`
	AccessToken  string          `json:"accessToken"`
	RefreshToken string          `json:"refreshToken"`
	ErrCode      json.RawMessage `json:"errCode"`
	ErrMsg       string          `json:"errMsg"`
}

func (c *Client) postLogin(ctx context.Context, payload map[string]any) (*Credentials, error) {
	body, status, err := c.postJSON(ctx, BaseURL+"/login", versionHeaders(), payload)
	if err != nil {
		return nil, fmt.Errorf("请求 /login 失败: %w", err)
	}
	var resp loginResp
	// 即使解析失败也继续走统一报错,给出服务端原文辅助定位。
	_ = json.Unmarshal(body, &resp)
	if status != http.StatusOK || resp.AccessToken == "" {
		return nil, fmt.Errorf("errCode=%s errMsg=%q (HTTP %d)",
			strings.TrimSpace(string(resp.ErrCode)), resp.ErrMsg, status)
	}
	creds := &Credentials{
		Vid:          rawString(resp.Vid),
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
	}
	if creds.Vid == "" {
		return nil, errors.New("服务端未返回 vid")
	}
	if creds.RefreshToken == "" {
		return nil, errors.New("服务端未返回 refreshToken")
	}
	return creds, nil
}
