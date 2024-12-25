// 运行日志的持久化:滚动保留最近 N 条,供日志页面查看。
package store

import (
	"database/sql"
	"time"
)

// LogRetention 是日志表的滚动保留条数,超出即淘汰最旧的。
const LogRetention = 1000

type LogEntry struct {
	ID      int64     `json:"id"`
	TS      int64     `json:"ts"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Alias   string    `json:"alias"`
	Message string    `json:"message"`
	Time    time.Time `json:"-"`
}

const logsSchema = `
CREATE TABLE IF NOT EXISTS weread_log (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts      INTEGER NOT NULL,
  level   TEXT NOT NULL DEFAULT 'info',
  source  TEXT NOT NULL DEFAULT '',
  alias   TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_weread_log_ts ON weread_log (ts DESC);
`

// AddLog 写入一条日志(尽力而为:日志失败不影响主流程),并按保留条数淘汰最旧的。
func AddLog(db *sql.DB, level, source, alias, message string) {
	if message == "" {
		return
	}
	switch level {
	case "warn", "error":
	default:
		level = "info"
	}
	res, err := db.Exec(`
		INSERT INTO weread_log (ts, level, source, alias, message)
		VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), level, source, alias, message)
	if err != nil {
		return
	}
	// 每写若干条才做一次淘汰检查,避免频繁全表扫;用 id 对 32 取整近似。
	if id, err := res.LastInsertId(); err == nil && id%32 == 0 {
		db.Exec(`DELETE FROM weread_log WHERE id NOT IN (SELECT id FROM weread_log ORDER BY id DESC LIMIT ?)`, LogRetention)
	}
}

// ListLogs 按条件查询日志,最新在前。
func ListLogs(db *sql.DB, alias, level string, limit int) ([]*LogEntry, error) {
	if limit <= 0 || limit > LogRetention {
		limit = 200
	}
	query := `SELECT id, ts, level, source, alias, message FROM weread_log WHERE 1=1`
	var args []any
	if alias != "" {
		query += ` AND alias = ?`
		args = append(args, alias)
	}
	if level != "" {
		query += ` AND level = ?`
		args = append(args, level)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LogEntry{}
	for rows.Next() {
		e := &LogEntry{}
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Source, &e.Alias, &e.Message); err != nil {
			return nil, err
		}
		e.Time = time.Unix(e.TS, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearLogs 清空全部日志。
func ClearLogs(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM weread_log`)
	return err
}
