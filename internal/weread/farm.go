// 阅读会话:按配置在书架选书,以 30 秒心跳模拟真实阅读推进,
// 心跳失败时自动续期(网页会话重新桥接 → 移动端凭据刷新)并继续。
package weread

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	farmHeartbeatSeconds    = 30 // 每次心跳间隔与自报时长,服务端按此记 0.5 分钟
	farmMaxConsecutiveFails = 3  // 连续失败上限,超过则中止会话
)

// FarmControl 允许外部暂停/继续/停止一场进行中的阅读会话。
// 零值不可用,须经 NewFarmControl 创建;可安全并发调用。
type FarmControl struct {
	mu      sync.Mutex
	paused  bool
	stopped bool
	wake    chan struct{}
}

func NewFarmControl() *FarmControl {
	return &FarmControl{wake: make(chan struct{})}
}

func (fc *FarmControl) changed() {
	close(fc.wake)
	fc.wake = make(chan struct{})
}

// Pause 暂停心跳;已停止的会话不可再暂停。
func (fc *FarmControl) Pause() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if !fc.paused && !fc.stopped {
		fc.paused = true
		fc.changed()
	}
}

// Resume 恢复暂停中的会话。
func (fc *FarmControl) Resume() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.paused {
		fc.paused = false
		fc.changed()
	}
}

// Stop 终止会话:暂停等待立即退出,心跳间隔的 sleep 经由 ctx 取消。
func (fc *FarmControl) Stop() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if !fc.stopped {
		fc.stopped = true
		fc.paused = false
		fc.changed()
	}
}

func (fc *FarmControl) IsPaused() bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.paused
}

func (fc *FarmControl) Stopped() bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.stopped
}

// WaitWhilePaused 阻塞直到恢复/停止/ctx 取消。
func (fc *FarmControl) WaitWhilePaused(ctx context.Context) error {
	for {
		fc.mu.Lock()
		if fc.stopped {
			fc.mu.Unlock()
			return context.Canceled
		}
		if !fc.paused {
			fc.mu.Unlock()
			return nil
		}
		wake := fc.wake
		fc.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// FarmResult 是一次阅读会话的结果汇总。
// Done 是本场累计成功记账的心跳数(含续跑起点),时长 = Done * 0.5 分钟。
type FarmResult struct {
	BookID   string
	Chapters int
	Done     int
	Total    int
	Err      string
}

type FarmProgress func(heartbeats, total int, msg string)

// FarmTask 描述一场阅读会话:读哪本书、总共多少次心跳、已完成多少次。
// Done > 0 即为续跑:接着上次的中断点继续刷。
type FarmTask struct {
	BookID string
	Done   int
	Total  int
}

// PickBook 从书单里随机挑一本(每天随机刷一本的需求点)。
func PickBook(bookIDs []string) string {
	if len(bookIDs) == 0 {
		return ""
	}
	return bookIDs[randIntn(len(bookIDs))]
}

// NewFarmTask 按书单与分钟数生成全新任务。
func NewFarmTask(bookIDs []string, minutes int) FarmTask {
	if minutes <= 0 {
		minutes = 30
	}
	return FarmTask{BookID: PickBook(bookIDs), Total: minutes * 2}
}

// FarmSession 执行一次阅读会话:从 bookIDs 随机选一本,阅读 minutes 分钟。
// 会话中途 Cookie 失效会自动续期(重新桥接;必要时刷新移动端凭据)继续刷。
// 返回的 next 非 nil 表示移动端凭据已轮换,调用方必须持久化。
// ctrl 可为 nil(如 CLI 场景);非 nil 时支持暂停/继续/停止。
func (c *Client) FarmSession(ctx context.Context, creds *Credentials, task FarmTask, onProgress FarmProgress, ctrl *FarmControl) (*FarmResult, *Credentials, error) {
	if task.BookID == "" {
		return nil, nil, errors.New("未选择要阅读的书籍")
	}
	if task.Total <= task.Done {
		// 续跑时发现已刷满,直接返回完成状态。
		return &FarmResult{BookID: task.BookID, Done: task.Done, Total: task.Total}, nil, nil
	}
	result := &FarmResult{BookID: task.BookID, Done: task.Done, Total: task.Total}

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

	chapterPos := randIntn(len(chapters))
	chapterUID := chapters[chapterPos]
	offset := randIntn(600)
	percent := 0
	consecutiveFails := 0

	for beat := task.Done; beat < task.Total; beat++ {
		if ctrl != nil {
			if err := ctrl.WaitWhilePaused(ctx); err != nil {
				result.Err = "已停止"
				return result, active, nil
			}
		}
		if beat > task.Done {
			if err := sleepCtx(ctx, farmHeartbeatSeconds*time.Second); err != nil {
				result.Err = "已取消"
				return result, active, nil
			}
		}
		hasSync, err := c.readHeartbeat(ctx, cookie, result.BookID, chapterUID, offset, percent, farmHeartbeatSeconds)
		if err == nil && !hasSync {
			// 无 synckey = 阅读同步状态未对齐,先调 chapterInfos 修复(参考实现的
			// fix_no_synckey 步骤),再原地重试本次心跳。
			if _, cerr := c.ChapterUIDs(ctx, cookie, result.BookID); cerr == nil {
				hasSync, err = c.readHeartbeat(ctx, cookie, result.BookID, chapterUID, offset, percent, farmHeartbeatSeconds)
			}
		}
		if err == nil && !hasSync {
			// 修复后仍未记账:视为会话失效,续期后原地重试本次心跳。
			cookie, active, err = c.renewSession(ctx, active, cookie)
			if err == nil {
				hasSync, err = c.readHeartbeat(ctx, cookie, result.BookID, chapterUID, offset, percent, farmHeartbeatSeconds)
			}
		}
		counted := err == nil && hasSync
		if !counted {
			if ctx.Err() != nil {
				result.Err = "已取消"
				return result, active, nil
			}
			consecutiveFails++
			if consecutiveFails >= farmMaxConsecutiveFails {
				reason := "服务端未记账"
				if err != nil {
					reason = err.Error()
				}
				result.Err = fmt.Sprintf("连续 %d 次上报失败,中止: %s", consecutiveFails, reason)
				return result, active, nil
			}
			if sleepErr := sleepCtx(ctx, 5*time.Second); sleepErr != nil {
				result.Err = "已取消"
				return result, active, nil
			}
			continue
		}
		consecutiveFails = 0
		result.Done = beat + 1
		if onProgress != nil {
			onProgress(result.Done, task.Total, fmt.Sprintf("已阅读 %.1f 分钟", float64(result.Done)*0.5))
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
// 返回的 active 是当前 Cookie 对应的凭据(供续期使用),无轮换时就是入参本身。
func (c *Client) webSession(ctx context.Context, creds *Credentials) (string, *Credentials, error) {
	cookie, err := c.WebCookie(ctx, creds)
	if err == nil {
		// 必须返回非 nil 的凭据:中途续期还要拿它重新桥接。
		return cookie, creds, nil
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
