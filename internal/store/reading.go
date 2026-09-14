// 阅读配置的持久化:每个账号一份"刷时长"配置与最近一次运行状态。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ReadingConfig 是单个账号的挑战赛配置。
type ReadingConfig struct {
	Vid         string   `json:"vid"`
	Enabled     bool     `json:"enabled"`
	BookIDs     []string `json:"book_ids"`
	Minutes     int      `json:"minutes"`
	RunAt       string   `json:"run_at"`        // 每日执行时间点 HH:MM(本地时区)
	LastRunDate string   `json:"last_run_date"` // 最近一次执行归属的日期 YYYY-MM-DD(用于每日去重)
	LastRunAt   int64    `json:"last_run_at"`   // 最近一次执行开始时刻
	LastStatus  string   `json:"last_status"`   // 最近一次执行结果描述
	// 断点续跑:当日会话被停止/中断后,据此接着刷
	RunBookID string `json:"run_book_id"`
	RunDone   int    `json:"run_done"`
	RunTotal  int    `json:"run_total"`
}

const readingSchema = `
CREATE TABLE IF NOT EXISTS weread_reading (
  vid           TEXT PRIMARY KEY,
  enabled       INTEGER NOT NULL DEFAULT 0,
  book_ids      TEXT NOT NULL DEFAULT '',
  minutes       INTEGER NOT NULL DEFAULT 30,
  run_at        TEXT NOT NULL DEFAULT '03:00',
  last_run_date TEXT NOT NULL DEFAULT '',
  last_run_at   INTEGER NOT NULL DEFAULT 0,
  last_status   TEXT NOT NULL DEFAULT '',
  run_book_id   TEXT NOT NULL DEFAULT '',
  run_done      INTEGER NOT NULL DEFAULT 0,
  run_total     INTEGER NOT NULL DEFAULT 0
);`

// DefaultReadingConfig 返回一份默认配置(未启用、30 分钟、凌晨 3 点)。
func DefaultReadingConfig(vid string) *ReadingConfig {
	return &ReadingConfig{
		Vid:     vid,
		Enabled: false,
		BookIDs: []string{},
		Minutes: 30,
		RunAt:   "03:00",
	}
}

// GetReadingConfig 读取配置;没有记录时返回默认值。
func GetReadingConfig(db *sql.DB, vid string) (*ReadingConfig, error) {
	row := db.QueryRow(`
		SELECT vid, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status,
		       run_book_id, run_done, run_total
		FROM weread_reading WHERE vid = ?`, vid)
	cfg := &ReadingConfig{}
	var enabled int
	var bookIDs string
	err := row.Scan(&cfg.Vid, &enabled, &bookIDs, &cfg.Minutes, &cfg.RunAt, &cfg.LastRunDate, &cfg.LastRunAt, &cfg.LastStatus,
		&cfg.RunBookID, &cfg.RunDone, &cfg.RunTotal)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultReadingConfig(vid), nil
	}
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled != 0
	cfg.BookIDs = splitBookIDs(bookIDs)
	return cfg, nil
}

// SaveReadingConfig UPSERT 配置项,不改动最近一次运行状态。
func SaveReadingConfig(db *sql.DB, cfg *ReadingConfig) error {
	if cfg.Vid == "" {
		return errors.New("vid 不能为空")
	}
	if cfg.Minutes <= 0 {
		cfg.Minutes = 30
	}
	if cfg.RunAt == "" {
		cfg.RunAt = "03:00"
	}
	_, err := db.Exec(`
		INSERT INTO weread_reading (vid, enabled, book_ids, minutes, run_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(vid) DO UPDATE SET
			enabled = excluded.enabled,
			book_ids = excluded.book_ids,
			minutes = excluded.minutes,
			run_at = excluded.run_at`,
		cfg.Vid, cfg.Enabled, joinBookIDs(cfg.BookIDs), cfg.Minutes, cfg.RunAt)
	return err
}

// ListEnabledReadingConfigs 列出所有启用了挑战赛的账号,供调度器扫描。
func ListEnabledReadingConfigs(db *sql.DB) ([]*ReadingConfig, error) {
	rows, err := db.Query(`
		SELECT vid, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status,
		       run_book_id, run_done, run_total
		FROM weread_reading WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReadingConfig{}
	for rows.Next() {
		cfg := &ReadingConfig{}
		var enabled int
		var bookIDs string
		if err := rows.Scan(&cfg.Vid, &enabled, &bookIDs, &cfg.Minutes, &cfg.RunAt, &cfg.LastRunDate, &cfg.LastRunAt, &cfg.LastStatus,
			&cfg.RunBookID, &cfg.RunDone, &cfg.RunTotal); err != nil {
			return nil, err
		}
		cfg.Enabled = enabled != 0
		cfg.BookIDs = splitBookIDs(bookIDs)
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// RunProgress 是阅读会话的断点信息,用于中断后续跑。
type RunProgress struct {
	BookID string
	Done   int
	Total  int
}

func nowUnix() int64 { return time.Now().Unix() }

// SaveReadingRunState 记录一次运行的归属日期与结果;progress 非 nil 时同步断点。
func SaveReadingRunState(db *sql.DB, vid, runDate, status string, progress *RunProgress) error {
	now := nowUnix()
	if progress != nil {
		_, err := db.Exec(`
			INSERT INTO weread_reading (vid, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status, run_book_id, run_done, run_total)
			VALUES (?, 0, '', 30, '03:00', ?, ?, ?, ?, ?, ?)
			ON CONFLICT(vid) DO UPDATE SET
				last_run_date = excluded.last_run_date,
				last_run_at = excluded.last_run_at,
				last_status = excluded.last_status,
				run_book_id = excluded.run_book_id,
				run_done = excluded.run_done,
				run_total = excluded.run_total`,
			vid, runDate, now, status, progress.BookID, progress.Done, progress.Total)
		return err
	}
	_, err := db.Exec(`
		INSERT INTO weread_reading (vid, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status)
		VALUES (?, 0, '', 30, '03:00', ?, ?, ?)
		ON CONFLICT(vid) DO UPDATE SET
			last_run_date = excluded.last_run_date,
			last_run_at = excluded.last_run_at,
			last_status = excluded.last_status,
 run_book_id = '', run_done = 0, run_total = 0`,
		vid, runDate, now, status)
	return err
}

// ResumeTask 返回当日未完成会话的续跑参数;没有可续跑的返回 ok=false。
func (c *ReadingConfig) ResumeTask() (bookID string, done, total int, ok bool) {
	if c.LastRunDate != time.Now().Format("2006-01-02") || c.RunDone < 0 || c.RunTotal <= 0 || c.RunDone >= c.RunTotal || c.RunBookID == "" {
		return "", 0, 0, false
	}
	return c.RunBookID, c.RunDone, c.RunTotal, true
}

func splitBookIDs(s string) []string {
	ids := []string{}
	if strings.HasPrefix(strings.TrimSpace(s), "[") {
		if json.Unmarshal([]byte(s), &ids) != nil {
			return []string{}
		}
	} else {
		ids = strings.Split(s, ",")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

func joinBookIDs(ids []string) string { return strings.Join(ids, ",") }
