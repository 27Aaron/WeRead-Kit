package weread

import (
	"context"
	"encoding/json"
	"errors"
)

// Details 是一次详情聚合采集的结果。
// Errs 记录单路失败(如网页桥接失败时 user/card 为空),不影响其它路。
type Details struct {
	User  json.RawMessage
	Card  json.RawMessage
	Shelf []ShelfBook
	Errs  map[string]string
}

// Details 采集用户信息、会员卡与书架。
// 任一路會話過期時自動用 refreshToken 續期一次;輪換出的憑據經 OnRefresh 持久化。
func (c *Client) Details(ctx context.Context, creds *Credentials) (*Details, error) {
	if d, expired := c.collectDetails(ctx, creds); !expired {
		return d, nil
	}
	next, err := c.Refresh(ctx, creds)
	if err != nil {
		return nil, errors.New("凭据已过期且续期失败,请重新扫码登录")
	}
	d, expired := c.collectDetails(ctx, next)
	if expired {
		return nil, errors.New("凭据已失效,请重新扫码登录")
	}
	return d, nil
}

// collectDetails 依序採集三路資料;任一路判定會話過期即回傳 expired=true。
func (c *Client) collectDetails(ctx context.Context, creds *Credentials) (*Details, bool) {
	d := &Details{Shelf: []ShelfBook{}, Errs: map[string]string{}}

	// 书架:移动端接口,现有凭据直连。
	shelf, err := c.ShelfSync(ctx, creds)
	switch {
	case err == nil:
		d.Shelf = shelf.Books
	case errors.Is(err, ErrSessionExpired):
		return nil, true
	default:
		d.Errs["shelf"] = err.Error()
	}

	// 用户/会员卡:网页版接口,先桥接网页会话。
	cookie, err := c.WebCookie(ctx, creds)
	switch {
	case errors.Is(err, ErrSessionExpired):
		return nil, true
	case err != nil:
		d.Errs["session"] = err.Error()
		return d, false
	}
	for _, part := range []struct {
		name  string
		dst   *json.RawMessage
		fetch func() (json.RawMessage, error)
	}{
		{"user", &d.User, func() (json.RawMessage, error) { return c.WebUserInfo(ctx, cookie, creds.Vid) }},
		{"card", &d.Card, func() (json.RawMessage, error) { return c.WebMemberCard(ctx, cookie) }},
	} {
		data, err := part.fetch()
		switch {
		case errors.Is(err, ErrSessionExpired):
			return nil, true
		case err != nil:
			d.Errs[part.name] = err.Error()
		default:
			*part.dst = data
		}
	}
	return d, false
}
