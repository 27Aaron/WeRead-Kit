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
	"net/http/cookiejar"
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
	var result struct {
		Succ int `json:"succ"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Succ != 1 {
		return "", fmt.Errorf("续期网页会话未成功")
	}
	merged := mergeWebCookies(cookie, resp.Cookies())
	request := &http.Request{Header: http.Header{"Cookie": []string{merged}}}
	if _, err := request.Cookie("wr_skey"); err != nil {
		return "", ErrSessionExpired
	}
	return merged, nil
}

func mergeWebCookies(cookie string, updates []*http.Cookie) string {
	// 每次合并使用独立 jar，账号之间不共享 Cookie。统一 Domain 后避免
	// host-only 与域 Cookie 同名共存；jar 负责 Path、Secure 和过期删除。
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(webBaseURL + "/web/book/read")
	req := &http.Request{Header: http.Header{"Cookie": []string{cookie}}}
	for _, ck := range req.Cookies() {
		ck.Domain = u.Hostname()
		ck.Path = "/"
		jar.SetCookies(u, []*http.Cookie{ck})
	}
	for _, ck := range updates {
		if ck == nil || (ck.Name != "wr_vid" && ck.Name != "wr_skey" && ck.Name != "wr_rt") {
			continue
		}
		if ck.Domain != "" && strings.TrimPrefix(ck.Domain, ".") != u.Hostname() {
			continue
		}
		copy := *ck
		copy.Domain = u.Hostname()
		jar.SetCookies(u, []*http.Cookie{&copy})
	}
	values := map[string]string{}
	for _, ck := range jar.Cookies(u) {
		if _, ok := values[ck.Name]; !ok {
			values[ck.Name] = ck.Value
		}
	}
	var out []string
	for _, name := range []string{"wr_vid", "wr_skey", "wr_rt"} {
		if v := values[name]; v != "" {
			out = append(out, name+"="+v)
		}
	}
	return strings.Join(out, "; ")
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
