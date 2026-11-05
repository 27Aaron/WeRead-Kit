// 阅读数据查询:书币余额。
package weread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// PayBalance 查询书币余额(balance 正常余额,giftBalance 赠币)。
// 参数为官方 App 固定组合,缺失会被 -2003 拒绝。
// 会话过期返回 ErrSessionExpired。
func (c *Client) PayBalance(ctx context.Context, creds *Credentials) (json.RawMessage, error) {
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	payload := map[string]any{
		"pf":            weeklyPF,
		"zoneid":        1,
		"release":       1,
		"noSnapshot":    0,
		"requireExpiry": 1,
	}
	data, status, err := c.postJSON(ctx, BaseURL+"/pay/balance", headers, payload)
	if err != nil {
		return nil, fmt.Errorf("书币余额请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("书币余额请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
