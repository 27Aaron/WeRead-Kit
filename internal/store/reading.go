// 阅读配置的持久化:每个账号一份"刷时长"配置与最近一次运行状态。
package store

import (
	"database/sql"
	"errors"
	"time"
)

// ReadingConfig 是单个账号的自动阅读配置。
type ReadingConfig struct {
	Alias       string   `json:"alias"`
	Enabled     bool     `json:"enabled"`
	BookIDs     []string `json:"book_ids"`
	Minutes     int      `json:"minutes"`
	RunAt       string   `json:"run_at"`        // 每日执行时间点 HH:MM(本地时区)
	LastRunDate string   `json:"last_run_date"` // 最近一次执行归属的日期 YYYY-MM-DD(用于每日去重)
	LastRunAt   int64    `json:"last_run_at"`   // 最近一次执行开始时刻
	LastStatus  string   `json:"last_status"`   // 最近一次执行结果描述
}

const readingSchema = `
CREATE TABLE IF NOT EXISTS weread_reading (
  alias         TEXT PRIMARY KEY,
  enabled       INTEGER NOT NULL DEFAULT 0,
  book_ids      TEXT NOT NULL DEFAULT '[]',
  minutes       INTEGER NOT NULL DEFAULT 30,
  run_at        TEXT NOT NULL DEFAULT '03:00',
  last_run_date TEXT NOT NULL DEFAULT '',
  last_run_at   INTEGER NOT NULL DEFAULT 0,
  last_status   TEXT NOT NULL DEFAULT ''
);`

// DefaultReadingConfig 返回一份默认配置(未启用、30 分钟、凌晨 3 点)。
func DefaultReadingConfig(alias string) *ReadingConfig {
	return &ReadingConfig{
		Alias:   alias,
		Enabled: false,
		BookIDs: []string{},
		Minutes: 30,
		RunAt:   "03:00",
	}
}

// GetReadingConfig 读取配置;没有记录时返回默认值。
func GetReadingConfig(db *sql.DB, alias string) (*ReadingConfig, error) {
	row := db.QueryRow(`
		SELECT alias, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status
		FROM weread_reading WHERE alias = ?`, alias)
	cfg := &ReadingConfig{}
	var enabled int
	var bookIDs string
	err := row.Scan(&cfg.Alias, &enabled, &bookIDs, &cfg.Minutes, &cfg.RunAt, &cfg.LastRunDate, &cfg.LastRunAt, &cfg.LastStatus)
	if errors.Is(err, sql.ErrNoRows) {
		def := DefaultReadingConfig(alias)
		return def, nil
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
	if cfg.Alias == "" {
		return errors.New("alias 不能为空")
	}
	if cfg.Minutes <= 0 {
		cfg.Minutes = 30
	}
	if cfg.RunAt == "" {
		cfg.RunAt = "03:00"
	}
	_, err := db.Exec(`
		INSERT INTO weread_reading (alias, enabled, book_ids, minutes, run_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(alias) DO UPDATE SET
			enabled = excluded.enabled,
			book_ids = excluded.book_ids,
			minutes = excluded.minutes,
			run_at = excluded.run_at`,
		cfg.Alias, boolToInt(cfg.Enabled), joinBookIDs(cfg.BookIDs), cfg.Minutes, cfg.RunAt)
	return err
}

// ListEnabledReadingConfigs 列出所有启用了自动阅读的账号,供调度器扫描。
func ListEnabledReadingConfigs(db *sql.DB) ([]*ReadingConfig, error) {
	rows, err := db.Query(`
		SELECT alias, enabled, book_ids, minutes, run_at, last_run_date, last_run_at, last_status
		FROM weread_reading WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReadingConfig
	for rows.Next() {
		cfg := &ReadingConfig{}
		var enabled int
		var bookIDs string
		if err := rows.Scan(&cfg.Alias, &enabled, &bookIDs, &cfg.Minutes, &cfg.RunAt, &cfg.LastRunDate, &cfg.LastRunAt, &cfg.LastStatus); err != nil {
			return nil, err
		}
		cfg.Enabled = enabled != 0
		cfg.BookIDs = splitBookIDs(bookIDs)
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// SaveReadingRunState 记录一次运行的归属日期与结果。
func SaveReadingRunState(db *sql.DB, alias, runDate string, status string) error {
	_, err := db.Exec(`
		INSERT INTO weread_reading (alias, last_run_date, last_run_at, last_status)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(alias) DO UPDATE SET
			last_run_date = excluded.last_run_date,
			last_run_at = excluded.last_run_at,
			last_status = excluded.last_status`,
		alias, runDate, time.Now().Unix(), status)
	return err
}

func splitBookIDs(s string) []string {
	out := []string{}
	current := ""
	for _, ch := range s {
		if ch == ',' {
			if current != "" {
				out = append(out, current)
			}
			current = ""
			continue
		}
		current += string(ch)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

func joinBookIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
