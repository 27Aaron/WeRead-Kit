package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// UpdateProfile 仅更新 Web UI 渲染所需的身份字段(昵称/头像/用户 ID)。
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

// SaveDetailsCache 仅持久化界面渲染所需的少量缓存字段,
// 易变的接口元数据、签名与支付类数据一律丢弃。
func SaveDetailsCache(db *sql.DB, alias string, profile, card, shelf []byte) error {
	profile = compactJSON(profile, []string{"userVid", "name", "avatar"})
	card = compactJSON(card, []string{"startTime", "expiredTime", "expired", "remainTime"})
	res, err := db.Exec(`UPDATE weread_account SET profile = ?, card = ?, shelf = ?, details_cached_at = ? WHERE alias = ?`, string(profile), string(card), string(shelf), time.Now().Unix(), alias)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// compactJSON 从原始 JSON 中摘取指定字段,重新序列化;解析失败时原样返回。
func compactJSON(raw []byte, fields []string) []byte {
	var src map[string]any
	if json.Unmarshal(raw, &src) != nil {
		return raw
	}
	dst := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, ok := src[field]; ok {
			dst[field] = value
		}
	}
	out, err := json.Marshal(dst)
	if err != nil {
		return raw
	}
	return out
}

