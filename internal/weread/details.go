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
// 任一路会话过期时自动用 refreshToken 续期一次;
// 返回的 next 非 nil 表示凭据已轮换,调用方必须把新凭据持久化,否则会丢会话。
func (c *Client) Details(ctx context.Context, creds *Credentials) (*Details, *Credentials, error) {
	d, expired, err := c.collectDetails(ctx, creds)
	if err != nil {
		return nil, nil, err
	}
	if !expired {
		return d, nil, nil
	}
	next, err := c.Refresh(ctx, creds)
	if err != nil {
		return nil, nil, errors.New("凭据已过期且续期失败,请重新扫码登录")
	}
	d, expired, err = c.collectDetails(ctx, next)
	if err != nil {
		return nil, next, err
	}
	if expired {
		return nil, next, errors.New("凭据已失效,请重新扫码登录")
	}
	return d, next, nil
}

func (c *Client) collectDetails(ctx context.Context, creds *Credentials) (*Details, bool, error) {
	d := &Details{Shelf: []ShelfBook{}, Errs: map[string]string{}}

	// 书架:移动端接口,现有凭据直连。
	shelf, err := c.ShelfSync(ctx, creds)
	switch {
	case err == nil:
		d.Shelf = shelf.Books
	case errors.Is(err, ErrSessionExpired):
		return nil, true, nil
	default:
		d.Errs["shelf"] = err.Error()
	}

	// 用户/会员卡:网页版接口,先桥接网页会话。
	cookie, err := c.WebCookie(ctx, creds)
	switch {
	case err == nil:
		for name, fetch := range map[string]func() (json.RawMessage, error){
			"user": func() (json.RawMessage, error) { return c.WebUserInfo(ctx, cookie, creds.Vid) },
			"card": func() (json.RawMessage, error) { return c.WebMemberCard(ctx, cookie) },
		} {
			data, err := fetch()
			switch {
			case err == nil:
				switch name {
				case "user":
					d.User = data
				case "card":
					d.Card = data
				}
			case errors.Is(err, ErrSessionExpired):
				return nil, true, nil
			default:
				d.Errs[name] = err.Error()
			}
		}
	case errors.Is(err, ErrSessionExpired):
		return nil, true, nil
	default:
		d.Errs["session"] = err.Error()
	}

	return d, false, nil
}
