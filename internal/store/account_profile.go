package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// UpdateProfile 只更新 Web UI 渲染所需的身份字段;备注为空时以昵称填充一次,手动设置的备注不会被覆盖。
func UpdateProfile(db *sql.DB, vid, name, avatar, userVid string) error {
	name = strings.TrimSpace(name)
	userVid = strings.TrimSpace(userVid)
	res, err := db.Exec(`UPDATE weread_account
		SET name = ?, avatar = ?, user_vid = ?, profile_updated_at = ?,
		    remark = CASE WHEN remark = '' THEN ? ELSE remark END
		WHERE vid = ?`,
		name, avatar, userVid, time.Now().Unix(), name, vid)
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

// SaveDetailsCache 只持久化界面渲染所需的少量缓存字段;
// 某一路为空(本次采集失败)时保留旧缓存,避免一次网络抖动清空书架与会员卡。
func SaveDetailsCache(db *sql.DB, vid string, profile, card, shelf []byte) error {
	profile = compactJSON(profile, []string{"userVid", "name", "avatar"})
	card = compactJSON(card, []string{"startTime", "expiredTime", "expired", "remainTime"})
	// 三路全空即本次采集全部失败:旧缓存原样保留,时间戳也不能刷新,
	// 否则详情页会把陈旧数据当"刚拉取"展示。
	if len(profile) == 0 && len(card) == 0 && len(shelf) == 0 {
		return nil
	}
	res, err := db.Exec(`UPDATE weread_account SET
			profile = COALESCE(NULLIF(?, ''), profile),
			card = COALESCE(NULLIF(?, ''), card),
			shelf = COALESCE(NULLIF(?, ''), shelf),
			details_cached_at = ?
		WHERE vid = ?`, string(profile), string(card), string(shelf), time.Now().Unix(), vid)
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
