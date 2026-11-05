// 官方阅读挑战:进度查询、可参与列表与免费挑战报名。
// challengeId 是字符串;免费挑战(如 21 天)不在 list 里,报名用固定 ID 直调 join。
package weread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ChallengeLongTermFree 是长期免费的 21 天阅读挑战的固定 challengeId:
// 21 天内累计阅读满 targetTime(10 小时)即挑战成功,勋章与奖励由服务端自动发放。
const ChallengeLongTermFree = "ChallengeLongTermFree"

// ErrChallengeAlreadyJoined 表示账号已在一场挑战中(join 返回 errcode -2904)。
var ErrChallengeAlreadyJoined = errors.New("已参与官方挑战赛")

// ChallengeDetail 查询官方阅读挑战的当前进度详情(报名中的那一场)。
// version=v3 返回 challengeList 数组,未报名时为空数组,由调用方自行判断;
// 早前用的 scene=1 实为签到接口,数据恒为空壳,已弃用。
// 会话过期返回 ErrSessionExpired,由调用方续期后重试。
func (c *Client) ChallengeDetail(ctx context.Context, creds *Credentials) (json.RawMessage, error) {
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	data, status, err := c.getJSON(ctx, BaseURL+"/challenge/detail?version=v3", headers)
	if err != nil {
		return nil, fmt.Errorf("挑战赛详情请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("挑战赛详情请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// ChallengeList 列出官方挑战赛的可参与项目。
// 注意:列表只包含付费挑战;免费的 21 天挑战用 ChallengeLongTermFree 直接报名。
// 会话过期返回 ErrSessionExpired,由调用方续期后重试。
func (c *Client) ChallengeList(ctx context.Context, creds *Credentials) (json.RawMessage, error) {
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	data, status, err := c.getJSON(ctx, BaseURL+"/challenge/list?version=v3", headers)
	if err != nil {
		return nil, fmt.Errorf("挑战赛列表请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("挑战赛列表请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// ChallengeJoin 报名官方挑战赛。仅免费挑战可走此接口;付费挑战(如
// readchallenge4/5)需应用内支付,服务端会以 -2003 拒绝纯 challengeId 报名。
// 已在挑战中返回 ErrChallengeAlreadyJoined;会话过期返回 ErrSessionExpired。
// 注意:错误信封是 errcode/errmsg(小写),与 weekly/exchange 的 errCode 不同;
// Go 的 json 解析大小写不敏感,统一用小写 tag 即可覆盖两种拼写。
func (c *Client) ChallengeJoin(ctx context.Context, creds *Credentials, challengeID string) (json.RawMessage, error) {
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	payload := map[string]any{"challengeId": challengeID}
	data, status, err := c.postJSON(ctx, BaseURL+"/challenge/join", headers, payload)
	if err != nil {
		return nil, fmt.Errorf("挑战赛报名请求失败: %w", err)
	}
	if status == http.StatusUnauthorized {
		return nil, ErrSessionExpired
	}
	// -2904(已参与)在 200 与非 200 响应里都可能出现,先于状态码判断。
	if code, _ := joinErrcode(data); code == "-2904" {
		return nil, ErrChallengeAlreadyJoined
	}
	if status != http.StatusOK {
		if code, msg := joinErrcode(data); code != "" {
			return nil, fmt.Errorf("挑战赛报名请求失败: HTTP %d errcode=%s errmsg=%q", status, code, msg)
		}
		return nil, fmt.Errorf("挑战赛报名请求失败: HTTP %d", status)
	}
	if err := checkBusinessCode(data); err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// joinErrcode 解析 join 的错误信封;无 errcode 字段时 code 为空串。
func joinErrcode(data []byte) (code, msg string) {
	var env struct {
		ErrCode json.Number `json:"errcode"`
		ErrMsg  string      `json:"errmsg"`
	}
	if json.Unmarshal(data, &env) != nil {
		return "", ""
	}
	return env.ErrCode.String(), env.ErrMsg
}
