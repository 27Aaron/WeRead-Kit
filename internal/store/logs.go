// 运行日志的持久化:滚动保留最近 N 条,供日志页面查看。
package store

import (
	"database/sql"
	"time"
)

// 双重上限:天数淘汰加行数硬上限兜底。
const (
	LogRetentionDays = 30
	LogMaxRows       = 20000
)

type LogEntry struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Vid     string `json:"vid"`
	Name    string `json:"name"` // 账号昵称,查询时关联账号表取得,供界面显示
	Message string `json:"message"`
}

const logsSchema = `
CREATE TABLE IF NOT EXISTS weread_log (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts      INTEGER NOT NULL,
  level   TEXT NOT NULL DEFAULT 'info',
  source  TEXT NOT NULL DEFAULT '',
  vid     TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_weread_log_ts ON weread_log (ts DESC);
CREATE INDEX IF NOT EXISTS idx_weread_log_vid_id ON weread_log (vid, id DESC);
`

// AddLog 写入一条日志;尽力而为,失败不影响主流程。
func AddLog(db *sql.DB, level, source, vid, message string) {
	if message == "" {
		return
	}
	switch level {
	case "warn", "error":
	default:
		level = "info"
	}
	res, err := db.Exec(`
		INSERT INTO weread_log (ts, level, source, vid, message)
		VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), level, source, vid, message)
	if err != nil {
		return
	}
	// 每写若干条才检查一次淘汰,避免频繁清理。
	if id, err := res.LastInsertId(); err == nil && id%32 == 0 {
		_, _ = db.Exec(`DELETE FROM weread_log WHERE ts < ?`, time.Now().AddDate(0, 0, -LogRetentionDays).Unix())
		// 按主键定位第 N 条之后的边界,避免 NOT IN 子查询物化 2 万个 id。
		_, _ = db.Exec(`DELETE FROM weread_log WHERE id <= (SELECT id FROM weread_log ORDER BY id DESC LIMIT 1 OFFSET ?)`, LogMaxRows)
	}
}

// ListLogs 按条件查询日志,最新在前。
func ListLogs(db *sql.DB, vid, level string, limit int) ([]*LogEntry, error) {
	if limit <= 0 || limit > LogMaxRows {
		limit = 200
	}
	// 展示名优先取备注,昵称兜底。
	query := `SELECT l.id, l.ts, l.level, l.source, l.vid, COALESCE(NULLIF(a.remark, ''), a.name, '') AS name, l.message
		FROM weread_log l
		LEFT JOIN weread_account a ON a.vid = l.vid
		WHERE 1=1`
	var args []any
	if vid != "" {
		query += ` AND l.vid = ?`
		args = append(args, vid)
	}
	if level != "" {
		query += ` AND l.level = ?`
		args = append(args, level)
	}
	query += ` ORDER BY l.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LogEntry{}
	for rows.Next() {
		e := &LogEntry{}
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Source, &e.Vid, &e.Name, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearLogs 清空全部日志。
func ClearLogs(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM weread_log`)
	return err
}
