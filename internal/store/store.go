// Package store 负责把微信读书凭据持久化到 SQLite。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound 表示该别名还没有登录过。
var ErrNotFound = errors.New("凭据不存在")

const schema = `
CREATE TABLE IF NOT EXISTS weread_account (
  alias         TEXT PRIMARY KEY,
  vid           TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  device_id     TEXT NOT NULL,
  access_token  TEXT NOT NULL DEFAULT '',
  remark        TEXT NOT NULL DEFAULT '',
  rotated_at    INTEGER NOT NULL,
  created_at    INTEGER NOT NULL
);`

// DefaultPath 返回默认数据库路径:当前目录下的 data/wxread.db,
// 即在项目根目录运行时落在 <项目根>/data/ 下。相对路径,随工作目录走。
func DefaultPath() (string, error) {
	return filepath.Join("data", "wxread.db"), nil
}

// Open 打开(必要时创建)数据库并执行迁移。
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	// modernc.org/sqlite 的 DSN 通过 _pragma 传连接级编译指令。
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}
	if _, err := db.Exec(readingSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化阅读配置表失败: %w", err)
	}
	if _, err := db.Exec(logsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化日志表失败: %w", err)
	}
	// 早期版本的库没有 remark 列,补上(表名列名是包内常量,无注入面)。
	if err := ensureColumn(db, "weread_account", "remark", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 remark 列失败: %w", err)
	}
	for _, column := range []string{"name", "avatar", "user_vid"} {
		if err := ensureColumn(db, "weread_account", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, fmt.Errorf("迁移用户资料失败: %w", err)
		}
	}
	if err := ensureColumn(db, "weread_account", "profile_updated_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, err
	}
	// 详情缓存:用户信息/会员卡/书架的原始 JSON,登录后预热,详情页免回源。
	for _, column := range []string{"profile", "card", "shelf"} {
		if err := ensureColumn(db, "weread_account", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, fmt.Errorf("迁移详情缓存列失败: %w", err)
		}
	}
	if err := ensureColumn(db, "weread_account", "details_cached_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, err
	}
	// 这个文件等于账号控制权,SQLite 新建文件不会自动收紧权限,WAL 伴生文件同理。
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
	return db, nil
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

// Credential 是 weread_account 表一行的内存表示。
type Credential struct {
	Alias            string
	Vid              string
	RefreshToken     string
	DeviceID         string
	AccessToken      string
	Remark           string
	Name             string
	Avatar           string
	UserVid          string
	ProfileUpdatedAt int64
	// 详情缓存:三段原始 JSON 与采集时间;空串表示还没有缓存。
	Profile         string
	Card            string
	Shelf           string
	DetailsCachedAt int64
	RotatedAt       time.Time
	CreatedAt       time.Time
}

const selectCols = `alias, vid, refresh_token, device_id, access_token, remark, rotated_at, created_at, name, avatar, user_vid, profile_updated_at, profile, card, shelf, details_cached_at`

func scanCredential(scan func(...any) error) (*Credential, error) {
	var c Credential
	var rotated, created int64
	if err := scan(&c.Alias, &c.Vid, &c.RefreshToken, &c.DeviceID, &c.AccessToken, &c.Remark, &rotated, &created, &c.Name, &c.Avatar, &c.UserVid, &c.ProfileUpdatedAt, &c.Profile, &c.Card, &c.Shelf, &c.DetailsCachedAt); err != nil {
		return nil, err
	}
	c.RotatedAt = time.Unix(rotated, 0)
	c.CreatedAt = time.Unix(created, 0)
	return &c, nil
}

func Load(db *sql.DB, alias string) (*Credential, error) {
	row := db.QueryRow(`SELECT `+selectCols+` FROM weread_account WHERE alias = ?`, alias)
	c, err := scanCredential(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// List 返回全部账号,按创建时间排序。
func List(db *sql.DB) ([]*Credential, error) {
	rows, err := db.Query(`SELECT ` + selectCols + ` FROM weread_account ORDER BY created_at, alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Credential
	for rows.Next() {
		c, err := scanCredential(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindAliasByVid 返回该 vid 已有的账号别名(最早创建的),没有则返回空串。
// Web 扫码登录用它把同一微信读书账号归并到既有记录,避免重复扫码产生重复行。
func FindAliasByVid(db *sql.DB, vid string) (string, error) {
	var alias string
	err := db.QueryRow(
		`SELECT alias FROM weread_account WHERE vid = ? ORDER BY created_at, alias LIMIT 1`, vid,
	).Scan(&alias)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return alias, nil
}

// Save 按 alias UPSERT 凭据,rotated_at 记录本次写入时间。
// 每次续期拿到新 refreshToken 后都必须立刻调用,否则丢了轮换值会话就断了。
// 注意冲突分支不更新 remark:token 轮换不能清掉用户写的备注。
func Save(db *sql.DB, c *Credential) error {
	if c.Alias == "" || c.Vid == "" || c.RefreshToken == "" || c.DeviceID == "" {
		return errors.New("凭据字段不完整,拒绝写入")
	}
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO weread_account (alias, vid, refresh_token, device_id, access_token, remark, rotated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(alias) DO UPDATE SET
			vid = excluded.vid,
			refresh_token = excluded.refresh_token,
			device_id = excluded.device_id,
			access_token = excluded.access_token,
			rotated_at = excluded.rotated_at`,
		c.Alias, c.Vid, c.RefreshToken, c.DeviceID, c.AccessToken, c.Remark, now, now)
	return err
}

// UpdateRemark 更新账号备注。
func UpdateRemark(db *sql.DB, alias, remark string) error {
	res, err := db.Exec(`UPDATE weread_account SET remark = ? WHERE alias = ?`, remark, alias)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除账号记录(不影响微信读书服务端的会话)。
func Delete(db *sql.DB, alias string) error {
	res, err := db.Exec(`DELETE FROM weread_account WHERE alias = ?`, alias)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateProfile saves display information independently of token rotation.
// The vid condition prevents an in-flight request overwriting a replaced account.
func UpdateProfile(db *sql.DB, alias, vid, name, avatar, userVid string) error {
	res, err := db.Exec(`UPDATE weread_account SET name = ?, avatar = ?, user_vid = ?, profile_updated_at = ? WHERE alias = ? AND vid = ?`, name, avatar, userVid, time.Now().Unix(), alias, vid)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveDetailsCache 持久化详情聚合数据(用户信息/会员卡/书架的原始 JSON)。
// 与凭据轮换写入互不覆盖:本函数只碰缓存列,Save 只碰凭据列。
func SaveDetailsCache(db *sql.DB, alias string, profile, card, shelf []byte) error {
	res, err := db.Exec(`
		UPDATE weread_account
		SET profile = ?, card = ?, shelf = ?, details_cached_at = ?
		WHERE alias = ?`,
		string(profile), string(card), string(shelf), time.Now().Unix(), alias)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}
