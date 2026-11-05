// 体验卡活动:官方通过 /act/sendinfinite 不定期赠送体验卡天数(如热门书单、飙升榜)。
// 查询与领取共用一个端点,靠 cmd 区分;各活动的参数组合不同,见活动注册表。
package weread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// SendInfiniteActivity 描述一个送体验卡活动及其查询/领取参数组合。
type SendInfiniteActivity struct {
	Name   string // 展示名(日志与通知用)
	Type   string // type 参数(查询始终需要)
	Source string // source 参数,活动的唯一标识
	// 领取时是否也带 type/guestAllow:热门书单需要(缺 type 报 -2003),飙升榜不带。
	ReceiveWithType bool
}

// SendInfiniteActivities 是已知的送卡活动清单。
var SendInfiniteActivities = []SendInfiniteActivity{
	{Name: "热门书单", Type: "hotlist", Source: "hotlist_infinite", ReceiveWithType: true},
	{Name: "飙升榜", Type: "soaring", Source: "soaring", ReceiveWithType: false},
}

// SendInfiniteState 兼容两个活动的响应字段:hotlist 用 received/day,
// soaring 用 canReceive/hadReceived/sendDays。
type SendInfiniteState struct {
	Day         int `json:"day"`
	Received    int `json:"received"`
	CanReceive  int `json:"canReceive"`
	HadReceived int `json:"hadReceived"`
	SendDays    int `json:"sendDays"`
}

// Claimable 判断当前是否可领。
func (s SendInfiniteState) Claimable() bool {
	if s.Received == 0 && s.Day > 0 {
		return true
	}
	return s.CanReceive == 1
}

// Days 返回本次活动赠送的天数。
func (s SendInfiniteState) Days() int {
	if s.Day > 0 {
		return s.Day
	}
	return s.SendDays
}

// sendInfinite 调 /act/sendinfinite。cmd=query 查询,receive 领取。
// 会话过期返回 ErrSessionExpired,由调用方续期后重试。
func (c *Client) sendInfinite(ctx context.Context, creds *Credentials, act SendInfiniteActivity, cmd string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("cmd", cmd)
	q.Set("source", act.Source)
	q.Set("pf", weeklyPF)
	if cmd == "query" || act.ReceiveWithType {
		q.Set("type", act.Type)
		q.Set("guestAllow", "1")
	}
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	data, status, err := c.getJSON(ctx, BaseURL+"/act/sendinfinite?"+q.Encode(), headers)
	if err != nil {
		return nil, fmt.Errorf("体验卡活动请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("体验卡活动请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// SendInfiniteQuery 查询活动状态(是否已领、可领天数)。
func (c *Client) SendInfiniteQuery(ctx context.Context, creds *Credentials, act SendInfiniteActivity) (SendInfiniteState, error) {
	var state SendInfiniteState
	data, err := c.sendInfinite(ctx, creds, act, "query")
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("体验卡活动响应解析失败: %w", err)
	}
	return state, nil
}

// SendInfiniteReceive 领取活动赠送的体验卡天数。无可领时服务端返回
// 业务错误(如 -2038),调用方按"无可领"处理。
func (c *Client) SendInfiniteReceive(ctx context.Context, creds *Credentials, act SendInfiniteActivity) (json.RawMessage, error) {
	return c.sendInfinite(ctx, creds, act, "receive")
}
