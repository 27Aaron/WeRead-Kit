// 阅读会话:按配置在书架选书,以 30 秒心跳模拟真实阅读推进,
// 心跳失败时自动续期(网页会话重新桥接 → 移动端凭据刷新)并继续。
package weread

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	farmHeartbeatSeconds    = 30 // 每次心跳间隔与自报时长,服务端按此记 0.5 分钟
	farmMaxConsecutiveFails = 3  // 连续失败上限,超过则中止会话
)

// FarmResult 是一次阅读会话的结果汇总。
type FarmResult struct {
	BookID     string
	Chapters   int
	Heartbeats int // 成功记账的心跳数,时长 = Heartbeats * 0.5 分钟
	Minutes    int // 配置的目标时长
	Err        string
}

type FarmProgress func(heartbeats, total int, msg string)

// FarmSession 执行一次阅读会话:从 bookIDs 随机选一本,阅读 minutes 分钟。
// 会话中途 Cookie 失效会自动续期(重新桥接;必要时刷新移动端凭据)继续刷。
// 返回的 next 非 nil 表示移动端凭据已轮换,调用方必须持久化。
func (c *Client) FarmSession(ctx context.Context, creds *Credentials, bookIDs []string, minutes int, onProgress FarmProgress) (*FarmResult, *Credentials, error) {
	if len(bookIDs) == 0 {
		return nil, nil, errors.New("未选择要阅读的书籍")
	}
	if minutes <= 0 {
		minutes = 30
	}
	result := &FarmResult{Minutes: minutes, BookID: bookIDs[randIntn(len(bookIDs))]}

	cookie, active, err := c.webSession(ctx, creds)
	if err != nil {
		result.Err = err.Error()
		return result, active, err
	}

	chapters, err := c.ChapterUIDs(ctx, cookie, result.BookID)
	if err != nil {
		cookie, active, err = c.renewSession(ctx, active, cookie)
		if err != nil {
			result.Err = err.Error()
			return result, active, err
		}
		chapters, err = c.ChapterUIDs(ctx, cookie, result.BookID)
		if err != nil {
			result.Err = err.Error()
			return result, active, err
		}
	}
	result.Chapters = len(chapters)

	total := minutes * 2 // 每 30 秒记 0.5 分钟
	chapterPos := randIntn(len(chapters))
	chapterUID := chapters[chapterPos]
	offset := randIntn(600)
	percent := 0
	consecutiveFails := 0

	for beat := 0; beat < total; beat++ {
		if beat > 0 {
			if err := sleepCtx(ctx, farmHeartbeatSeconds*time.Second); err != nil {
				result.Err = "已取消"
				result.Heartbeats = beat
				return result, active, nil
			}
		}
		hasSync, err := c.readHeartbeat(ctx, cookie, result.BookID, chapterUID, offset, percent, farmHeartbeatSeconds)
		if err == nil && !hasSync {
			// 服务端未记账:视为会话失效,续期后原地重试本次心跳。
			cookie, active, err = c.renewSession(ctx, active, cookie)
			if err == nil {
				hasSync, err = c.readHeartbeat(ctx, cookie, result.BookID, chapterUID, offset, percent, farmHeartbeatSeconds)
			}
		}
		counted := err == nil && hasSync
		if !counted {
			if ctx.Err() != nil {
				result.Err = "已取消"
				result.Heartbeats = beat
				return result, active, nil
			}
			consecutiveFails++
			if consecutiveFails >= farmMaxConsecutiveFails {
				reason := "服务端未记账"
				if err != nil {
					reason = err.Error()
				}
				result.Err = fmt.Sprintf("连续 %d 次上报失败,中止: %s", consecutiveFails, reason)
				result.Heartbeats = beat
				return result, active, nil
			}
			if sleepErr := sleepCtx(ctx, 5*time.Second); sleepErr != nil {
				result.Err = "已取消"
				result.Heartbeats = beat
				return result, active, nil
			}
			continue
		}
		consecutiveFails = 0
		result.Heartbeats = beat + 1
		if onProgress != nil {
			onProgress(beat+1, total, fmt.Sprintf("已阅读 %.1f 分钟", float64(beat+1)*0.5))
		}

		// 推进阅读位置:章节内偏移前进,超过章节长度就翻到下一章。
		offset += 200 + randIntn(400)
		percent = offset / 18
		if percent > 99 {
			percent = 99
		}
		if offset > 1800 {
			chapterPos = (chapterPos + 1) % len(chapters)
			chapterUID = chapters[chapterPos]
			offset = randIntn(300)
			percent = 0
		}
	}
	return result, active, nil
}

// webSession 获取网页会话 Cookie;移动端凭据过期时先刷新再桥接。
func (c *Client) webSession(ctx context.Context, creds *Credentials) (string, *Credentials, error) {
	cookie, err := c.WebCookie(ctx, creds)
	if err == nil {
		return cookie, nil, nil
	}
	if !errors.Is(err, ErrSessionExpired) {
		// 桥接失败未必是凭据过期,也重试一次刷新路径。
		next, rerr := c.Refresh(ctx, creds)
		if rerr != nil {
			return "", nil, fmt.Errorf("桥接网页会话失败: %w", err)
		}
		cookie, err = c.WebCookie(ctx, next)
		if err != nil {
			return "", next, fmt.Errorf("桥接网页会话失败: %w", err)
		}
		return cookie, next, nil
	}
	next, rerr := c.Refresh(ctx, creds)
	if rerr != nil {
		return "", nil, fmt.Errorf("凭据过期且续期失败: %w", rerr)
	}
	cookie, err = c.WebCookie(ctx, next)
	if err != nil {
		return "", next, fmt.Errorf("续期后桥接仍失败: %w", err)
	}
	return cookie, next, nil
}

// renewSession 心跳中途会话失效时:重新桥接;桥接不动就刷新移动端凭据再桥接。
func (c *Client) renewSession(ctx context.Context, active *Credentials, _ string) (string, *Credentials, error) {
	return c.webSession(ctx, active)
}
