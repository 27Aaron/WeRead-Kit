// Package store 负责把微信读书凭据持久化到 SQLite。
// 账号以 vid(微信读书用户 ID)为唯一键。
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

// ErrNotFound 表示该账号不存在。
var ErrNotFound = errors.New("账号不存在")

const schema = `
CREATE TABLE IF NOT EXISTS weread_account (
  vid           TEXT PRIMARY KEY,
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
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	// 这个文件等于账号控制权,SQLite 新建文件不会自动收紧权限,WAL 伴生文件同理。
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
	return db, nil
}

// migrate 依次执行:建表 → 旧结构迁移 → 补列。
func migrate(db *sql.DB) error {
	for _, stmt := range []string{schema, readingSchema, logsSchema, pushSchema} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	// 旧版账号表以 alias 为主键;统一迁移为以 vid 为主键。
	if err := migrateAliasKeyToVid(db); err != nil {
		return err
	}
	// 渐进补列:老库逐版本演进过程中缺失的列都在这里补齐。
	for _, column := range []struct{ table, name, def string }{
		{"weread_account", "remark", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "name", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "avatar", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "user_vid", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "profile", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "card", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "shelf", "TEXT NOT NULL DEFAULT ''"},
		{"weread_account", "details_cached_at", "INTEGER NOT NULL DEFAULT 0"},
		{"weread_account", "profile_updated_at", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(db, column.table, column.name, column.def); err != nil {
			return err
		}
	}
	return nil
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

// migrateAliasKeyToVid 把旧版以 alias 为主键的账号表、阅读配置表
// 重建为以 vid 为主键;日志表的 alias 列更名为 vid,值同步改写。
func migrateAliasKeyToVid(db *sql.DB) error {
	var hasAlias int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('weread_account') WHERE name = 'alias'`).Scan(&hasAlias); err != nil {
		return err
	}
	if hasAlias == 0 {
		return nil // 已是新结构
	}

	// alias → vid 映射:阅读配置与日志都要据此改写
	amap := map[string]string{}
	rows, err := db.Query(`SELECT alias, vid FROM weread_account`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a, v string
		if err := rows.Scan(&a, &v); err != nil {
			rows.Close()
			return err
		}
		amap[a] = v
	}
	rows.Close()

	// 阅读配置表:主键改写,映射不到的行丢弃
	if _, err := db.Exec(`DROP TABLE weread_reading`); err != nil {
		return err
	}
	if _, err := db.Exec(readingSchema); err != nil {
		return err
	}
	for oldKey, vid := range amap {
		if _, err := db.Exec(`
			INSERT INTO weread_reading (vid, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status, run_book_id, run_done, run_total)
			SELECT ?, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status, run_book_id, run_done, run_total
			FROM weread_reading WHERE alias = ?`, vid, oldKey); err != nil {
			return err
		}
	}

	// 日志:alias 值改写为 vid,随后列名同步更名
	for a, v := range amap {
		if _, err := db.Exec(`UPDATE weread_log SET alias = ? WHERE alias = ?`, v, a); err != nil {
			return err
		}
	}
	if err := ensureColumn(db, "weread_log", "vid", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE weread_log SET vid = alias WHERE vid = ''`); err != nil {
		return err
	}

	// 账号表重建:去掉 alias 列,vid 升为主键
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS weread_account_new (
  vid           TEXT PRIMARY KEY,
  refresh_token TEXT NOT NULL,
  device_id     TEXT NOT NULL,
  access_token  TEXT NOT NULL DEFAULT '',
  remark        TEXT NOT NULL DEFAULT '',
  rotated_at    INTEGER NOT NULL,
  created_at    INTEGER NOT NULL
);`); err != nil {
		return err
	}
	if _, err := db.Exec(`
INSERT INTO weread_account_new (vid, refresh_token, device_id, access_token, remark, rotated_at, created_at)
SELECT vid, refresh_token, device_id, access_token, remark, rotated_at, created_at FROM weread_account`); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE weread_account`); err != nil {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE weread_account_new RENAME TO weread_account`); err != nil {
		return err
	}
	return nil
}

// Credential 是 weread_account 表一行的内存表示。
type Credential struct {
	Vid          string
	RefreshToken string
	DeviceID     string
	AccessToken  string
	Remark       string
	Name         string
	Avatar       string
	UserVid      string
	// 详情缓存:三段原始 JSON 与采集时间;空串表示还没有缓存。
	Profile         string
	Card            string
	Shelf           string
	DetailsCachedAt int64
	ProfileUpdatedAt int64
	RotatedAt       time.Time
	CreatedAt       time.Time
}

const selectCols = `vid, refresh_token, device_id, access_token, remark, name, avatar, user_vid, profile, card, shelf, rotated_at, created_at, details_cached_at, profile_updated_at`

func scanCredential(scan func(...any) error) (*Credential, error) {
	var c Credential
	var rotated, created int64
	if err := scan(&c.Vid, &c.RefreshToken, &c.DeviceID, &c.AccessToken, &c.Remark, &c.Name, &c.Avatar, &c.UserVid, &c.Profile, &c.Card, &c.Shelf, &rotated, &created, &c.DetailsCachedAt, &c.ProfileUpdatedAt); err != nil {
		return nil, err
	}
	c.RotatedAt = time.Unix(rotated, 0)
	c.CreatedAt = time.Unix(created, 0)
	return &c, nil
}

func Load(db *sql.DB, vid string) (*Credential, error) {
	row := db.QueryRow(`SELECT `+selectCols+` FROM weread_account WHERE vid = ?`, vid)
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
	rows, err := db.Query(`SELECT ` + selectCols + ` FROM weread_account ORDER BY created_at`)
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

// Save 按 vid UPSERT 凭据,rotated_at 记录本次写入时间。
// 每次续期拿到新 refreshToken 后都必须立刻调用,否则丢了轮换值会话就断了。
// 注意冲突分支不更新 remark/name:token 轮换不能清掉用户设置的展示信息。
func Save(db *sql.DB, c *Credential) error {
	if c.Vid == "" || c.RefreshToken == "" || c.DeviceID == "" {
		return errors.New("凭据字段不完整,拒绝写入")
	}
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO weread_account (vid, refresh_token, device_id, access_token, remark, rotated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(vid) DO UPDATE SET
			refresh_token = excluded.refresh_token,
			device_id = excluded.device_id,
			access_token = excluded.access_token,
			rotated_at = excluded.rotated_at`,
		c.Vid, c.RefreshToken, c.DeviceID, c.AccessToken, c.Remark, now, now)
	return err
}

// UpdateRemark 更新账号备注(界面上的显示名)。
func UpdateRemark(db *sql.DB, vid, remark string) error {
	res, err := db.Exec(`UPDATE weread_account SET remark = ? WHERE vid = ?`, remark, vid)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除账号记录(不影响微信读书服务端的会话)。
func Delete(db *sql.DB, vid string) error {
	res, err := db.Exec(`DELETE FROM weread_account WHERE vid = ?`, vid)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}
