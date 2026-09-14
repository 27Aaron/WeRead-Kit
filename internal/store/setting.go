// 全局键值设置:存放跨重启需要保持的服务端状态(如 Web 会话签名密钥)。
package store

import (
	"database/sql"
	"errors"
)

const settingsSchema = `
CREATE TABLE IF NOT EXISTS weread_setting (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);`

// GetSetting 读取设置项;不存在时返回 ErrNotFound。
func GetSetting(db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM weread_setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting UPSERT 设置项。
func SetSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`
		INSERT INTO weread_setting (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
