// 网页版接口桥接:用移动端凭据(vid/accessToken/refreshToken)换取网页会话 Cookie,
// 再以 Cookie 调用仅网页版提供的接口(用户信息、会员卡)。
package weread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	webBaseURL   = "https://weread.qq.com"
	webUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// RenewWebCookie 续期现有网页会话。成功时合并服务端返回的 Set-Cookie，
// 保留未被轮换的 wr_vid/wr_rt。
func (c *Client) RenewWebCookie(ctx context.Context, cookie string) (string, error) {
	if cookie == "" {
		return "", fmt.Errorf("网页 Cookie 为空")
	}
	body, err := json.Marshal(map[string]any{"rq": "%2Fweb%2Fbook%2Fread", "ql": false})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webBaseURL+"/web/login/renewal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Origin", webBaseURL)
	req.Header.Set("Referer", webBaseURL+"/")
	req.Header.Set("Cookie", cookie)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("续期网页会话失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("续期网页会话失败: HTTP %d", resp.StatusCode)
	}
	if err := checkBusinessCode(data); err != nil {
		return "", err
	}
	return mergeWebCookies(cookie, resp.Cookies()), nil
}

func mergeWebCookies(cookie string, updates []*http.Cookie) string {
	values := map[string]string{}
	for _, part := range strings.Split(cookie, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			values[kv[0]] = kv[1]
		}
	}
	for _, ck := range updates {
		if ck != nil && (ck.Name == "wr_vid" || ck.Name == "wr_skey" || ck.Name == "wr_rt") {
			values[ck.Name] = ck.Value
		}
	}
	out := ""
	for _, name := range []string{"wr_vid", "wr_skey", "wr_rt"} {
		if v := values[name]; v != "" {
			if out != "" {
				out += "; "
			}
			out += name + "=" + v
		}
	}
	return out
}

// WebCookie 用移动端凭据调 /web/login/session/init 换取网页会话。
// 服务端通过 set-cookie 下发 wr_vid / wr_skey / wr_rt,拼成 Cookie 串返回。
func (c *Client) WebCookie(ctx context.Context, creds *Credentials) (string, error) {
	if creds == nil || creds.AccessToken == "" || creds.RefreshToken == "" {
		return "", fmt.Errorf("凭据不完整,无法桥接网页会话")
	}
	payload, err := json.Marshal(map[string]any{
		"vid":  creds.Vid,
		"pf":   0,
		"skey": creds.AccessToken,
		"rt":   creds.RefreshToken,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webBaseURL+"/web/login/session/init", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", webUserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("桥接网页会话失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("桥接网页会话失败: HTTP %d", resp.StatusCode)
	}

	// 桥接失败最常见的原因是移动端凭据过期(-2012),交给上层走续期重试。
	var envelope struct {
		ErrCode json.Number `json:"errCode"`
		ErrMsg  string      `json:"errMsg"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.ErrCode.String() == "-2012" {
		return "", ErrSessionExpired
	}

	values := map[string]string{}
	for _, ck := range resp.Cookies() {
		switch ck.Name {
		case "wr_vid", "wr_skey", "wr_rt":
			values[ck.Name] = ck.Value
		}
	}
	if values["wr_skey"] == "" {
		return "", fmt.Errorf("桥接网页会话失败: 服务端未下发 wr_skey (HTTP %d errCode=%s errMsg=%q)",
			resp.StatusCode, envelope.ErrCode.String(), envelope.ErrMsg)
	}
	cookie := ""
	for _, name := range []string{"wr_vid", "wr_skey", "wr_rt"} {
		if v, ok := values[name]; ok {
			if cookie != "" {
				cookie += "; "
			}
			cookie += name + "=" + v
		}
	}
	return cookie, nil
}

// WebUserInfo 查询用户信息(网页版接口)。
func (c *Client) WebUserInfo(ctx context.Context, cookie, vid string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("userVid", vid)
	return c.webGet(ctx, "/web/user", q, cookie)
}

// WebMemberCard 查询会员卡信息(网页版接口)。
func (c *Client) WebMemberCard(ctx context.Context, cookie string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("pf", "ios")
	return c.webGet(ctx, "/web/pay/memberCardSummary", q, cookie)
}

func (c *Client) webGet(ctx context.Context, path string, query url.Values, cookie string) (json.RawMessage, error) {
	u := webBaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return c.webCall(ctx, http.MethodGet, u, nil, cookie)
}

func (c *Client) webCall(ctx context.Context, method, u string, body []byte, cookie string) (json.RawMessage, error) {
	data, status, err := c.do(ctx, method, u, map[string]string{
		"User-Agent": webUserAgent,
		"cookie":     cookie,
	}, body)
	if err != nil {
		return nil, fmt.Errorf("网页接口 %s 失败: %w", u, err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("网页接口 %s 失败: HTTP %d", u, status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
