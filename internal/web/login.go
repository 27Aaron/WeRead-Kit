package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"wxread/internal/store"
	"wxread/internal/weread"
)

// sessionTTL 是登录会话在内存中的保留时长:覆盖 5 分钟扫码窗口,
// 并给前端足够时间读到最终状态。
const sessionTTL = 15 * time.Minute

// loginSession 表示一次进行中的扫码登录。
// 扫码前不知道账号身份,alias 在登录成功后按 vid 归并解析得到。
type loginSession struct {
	alias      string // 扫码成功后才解析
	remark     string
	confirmURL string
	status     string // pending | scanned | success | expired | declined | canceled | error
	errMsg     string
	creds      *weread.Credentials
	createdAt  time.Time
	cancel     context.CancelFunc
}

// loginManager 管理并发登录会话:每个会话由后台 goroutine 驱动扫码流程,
// 前端通过轮询会话状态渲染进度。
type loginManager struct {
	mu       sync.Mutex
	sessions map[string]*loginSession
	db       *sql.DB
	client   *weread.Client
}

func newLoginManager(db *sql.DB, client *weread.Client) *loginManager {
	return &loginManager{sessions: map[string]*loginSession{}, db: db, client: client}
}

func (m *loginManager) start(remark string) (string, error) {
	m.gc()
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	sess := &loginSession{
		remark:    remark,
		status:    "pending",
		createdAt: time.Now(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	go func() {
		defer cancel()
		creds, err := m.client.Login(ctx, "",
			func(confirmURL string) { m.setConfirmURL(id, confirmURL) },
			func(string) { m.setStatus(id, "scanned") })
		m.finish(id, creds, err)
	}()
	return id, nil
}

func (m *loginManager) get(id string) (loginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return loginSession{}, false
	}
	return *sess, true
}

// waitQR 阻塞等待二维码链接就绪(票据与 qrconnect 有几百毫秒网络往返),
// 或直到会话进入终态/超时。就绪后调用方再拿到的 qr_url 必定可加载。
func (m *loginManager) waitQR(id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		sess, ok := m.get(id)
		if !ok {
			return errors.New("登录会话不存在")
		}
		if sess.confirmURL != "" {
			return nil
		}
		switch sess.status {
		case "pending", "scanned":
			// 二维码还没到,继续等。
		default:
			return errors.New(sess.errMsg)
		}
		if time.Now().After(deadline) {
			return errors.New("二维码生成超时")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *loginManager) setConfirmURL(id, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[id]; ok {
		sess.confirmURL = url
	}
}

func (m *loginManager) setStatus(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[id]; ok {
		sess.status = status
	}
}

// finish 收尾一次登录:落库并标记最终状态。
// alias/remark 在 start 之后不可变,可安全在锁外读取;
// 状态字段的写入全部收进锁内,与 get 的读取互斥。
func (m *loginManager) finish(id string, creds *weread.Credentials, err error) {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return
	}

	fail := func(status, msg string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		sess.status, sess.errMsg = status, msg
		if status == "error" {
			store.AddLog(m.db, "error", "auth", sess.alias, "扫码登录失败: "+msg)
		}
	}

	switch {
	case err == nil:
		// 别名归并:同一 vid 复用既有记录(无论它当初是 CLI 还是网页创建的),
		// 全新账号则直接以 vid 作为别名,重复扫码天然幂等。
		alias, dbErr := store.FindAliasByVid(m.db, creds.Vid)
		if dbErr != nil {
			fail("error", "查询已有账号失败: "+dbErr.Error())
			return
		}
		if alias == "" {
			alias = creds.Vid
		}
		record := &store.Credential{
			Alias:        alias,
			Vid:          creds.Vid,
			RefreshToken: creds.RefreshToken,
			DeviceID:     creds.DeviceID,
			AccessToken:  creds.AccessToken,
			Remark:       sess.remark, // 新账号 INSERT 时直接带上备注
		}
		if dbErr := store.Save(m.db, record); dbErr != nil {
			fail("error", "凭据写入数据库失败: "+dbErr.Error())
			return
		}
		// 老账号走 UPSERT 冲突分支不会更新 remark,这里单独补写。
		if sess.remark != "" {
			if dbErr := store.UpdateRemark(m.db, alias, sess.remark); dbErr != nil && !errors.Is(dbErr, store.ErrNotFound) {
				fail("error", "备注写入数据库失败: "+dbErr.Error())
				return
			}
		}
		// Credentials are already durable. Profile failures must not undo login.
		store.AddLog(m.db, "info", "auth", alias, "扫码登录成功,凭据已更新")
		go m.warmDetails(record)
		m.mu.Lock()
		sess.alias = alias
		sess.status = "success"
		sess.creds = creds
		m.mu.Unlock()
	case errors.Is(err, weread.ErrQRExpired):
		fail("expired", err.Error())
	case errors.Is(err, weread.ErrQRDeclined):
		fail("declined", err.Error())
	case errors.Is(err, context.Canceled):
		fail("canceled", "登录已取消")
	default:
		fail("error", err.Error())
	}
}

// gc 清理超时会话,释放内存并取消仍在轮询的 goroutine。
// warmDetails 在登录成功后异步拉一次详情(用户信息/会员卡/书架)写入缓存,
// 让详情页首次打开就是秒开。失败静默:前端「刷新数据」随时可以强制回源。
func (m *loginManager) warmDetails(record *store.Credential) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, next, err := m.client.Details(ctx, toWeread(record))
	if err != nil {
		return
	}
	if next != nil {
		updated := toStore(record.Alias, next)
		updated.Remark = record.Remark
		if err := store.Save(m.db, updated); err != nil {
			return
		}
		record = updated
	}
	shelfJSON, err := json.Marshal(d.Shelf)
	if err != nil {
		return
	}
	if err := store.SaveDetailsCache(m.db, record.Alias, d.User, d.Card, shelfJSON); err != nil {
		return
	}
	_ = saveProfile(m.db, record, d.User)
}

func (m *loginManager) gc() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, sess := range m.sessions {
		if time.Since(sess.createdAt) > sessionTTL {
			if sess.cancel != nil {
				sess.cancel()
			}
			delete(m.sessions, id)
		}
	}
}

func newSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
