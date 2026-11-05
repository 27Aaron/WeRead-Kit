// 勋章:查询账号已获得与未解锁的勋章列表。
package weread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// MedalList 查询账号的勋章列表:available(已获得,含名称/排名/获得时间)、
// notAvailable(未解锁,按分类分组)、wear(当前佩戴)、totalCount。
// 会话过期返回 ErrSessionExpired,由调用方续期后重试。
func (c *Client) MedalList(ctx context.Context, creds *Credentials) (json.RawMessage, error) {
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	data, status, err := c.getJSON(ctx, BaseURL+"/medal/list", headers)
	if err != nil {
		return nil, fmt.Errorf("勋章列表请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("勋章列表请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
