// 运行日志的持久化:滚动保留最近 N 条,供日志页面查看。
package store

import (
	"database/sql"
	"time"
)

// 日志滚动保留:超过 60 天的淘汰,同时保留 2 万条硬上限兜底。
const (
	LogRetentionDays = 30
	LogMaxRows       = 20000
)

type LogEntry struct {
	ID      int64     `json:"id"`
	TS      int64     `json:"ts"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Vid     string    `json:"vid"`
	Name    string    `json:"name"` // 账号昵称,关联账号表得到,供界面展示
	Message string    `json:"message"`
	Time    time.Time `json:"-"`
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
`

// AddLog 写入一条日志(尽力而为:日志失败不影响主流程),并按保留条数淘汰最旧的。
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
	// 每写若干条才做一次淘汰检查,避免频繁清理;用 id 对 32 取整近似。
	if id, err := res.LastInsertId(); err == nil && id%32 == 0 {
		db.Exec(`DELETE FROM weread_log WHERE ts < ?`, time.Now().AddDate(0, 0, -LogRetentionDays).Unix())
		db.Exec(`DELETE FROM weread_log WHERE id NOT IN (SELECT id FROM weread_log ORDER BY id DESC LIMIT ?)`, LogMaxRows)
	}
}

// ListLogs 按条件查询日志,最新在前。
func ListLogs(db *sql.DB, vid, level string, limit int) ([]*LogEntry, error) {
	if limit <= 0 || limit > LogMaxRows {
		limit = 200
	}
	// 展示名优先取备注(可能为用户手动设置,也可能由昵称自动填充),昵称兜底。
	query := `SELECT l.id, l.ts, l.level, l.source, l.vid, COALESCE(NULLIF(a.remark, ''), a.name, '') AS name, l.message
		FROM weread_log l
		LEFT JOIN weread_account a ON a.vid = l.vid
		WHERE 1=1`
	var args []any
	if vid != "" {
		query += ` AND vid = ?`
		args = append(args, vid)
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
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Source, &e.Vid, &e.Name, &e.Message); err != nil {
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
