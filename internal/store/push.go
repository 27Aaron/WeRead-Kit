// 推送渠道配置的持久化:四类渠道各固定一行,按类型存取,支持多渠道同时广播。
package store

import (
	"database/sql"
	"errors"
	"time"
)

// PushChannel 是单个推送渠道的配置。
type PushChannel struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// Params 是渠道参数的 JSON 串(各渠道字段不同),保存前由调用方校验。
	Params string `json:"params"`
}

const pushSchema = `
CREATE TABLE IF NOT EXISTS weread_push_channel (
  type       TEXT PRIMARY KEY,
  enabled    INTEGER NOT NULL DEFAULT 0,
  params     TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL DEFAULT 0
);`

func ListPushChannels(db *sql.DB) ([]*PushChannel, error) {
	rows, err := db.Query(`SELECT type, enabled, params FROM weread_push_channel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PushChannel{}
	for rows.Next() {
		c := &PushChannel{}
		var enabled int
		if err := rows.Scan(&c.Type, &enabled, &c.Params); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func ListEnabledPushChannels(db *sql.DB) ([]*PushChannel, error) {
	rows, err := db.Query(`SELECT type, enabled, params FROM weread_push_channel WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PushChannel{}
	for rows.Next() {
		c := &PushChannel{}
		var enabled int
		if err := rows.Scan(&c.Type, &enabled, &c.Params); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPushChannel 读取单个渠道配置;没有保存过时返回 ErrNotFound。
func GetPushChannel(db *sql.DB, chType string) (*PushChannel, error) {
	row := db.QueryRow(`SELECT type, enabled, params FROM weread_push_channel WHERE type = ?`, chType)
	c := &PushChannel{Type: chType}
	var enabled int
	err := row.Scan(&c.Type, &enabled, &c.Params)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	return c, nil
}

// SavePushChannel 按类型 UPSERT 渠道配置。
// 采用先查后写而非 ON CONFLICT:兼容早期以自增 id 为主键的旧表结构。
func SavePushChannel(db *sql.DB, c *PushChannel) error {
	if c.Type == "" {
		return errors.New("渠道类型不能为空")
	}
	var id int64
	err := db.QueryRow(`SELECT id FROM weread_push_channel WHERE type = ?`, c.Type).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.Exec(`
			INSERT INTO weread_push_channel (type, enabled, params, created_at)
			VALUES (?, ?, ?, ?)`,
			c.Type, boolToInt(c.Enabled), c.Params, time.Now().Unix())
		return err
	case err != nil:
		return err
	}
	_, err = db.Exec(`
		UPDATE weread_push_channel SET enabled = ?, params = ? WHERE id = ?`,
		boolToInt(c.Enabled), c.Params, id)
	return err
}
