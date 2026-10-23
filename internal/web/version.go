package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Version = "0.1.12"

// 只比较稳定版本的数字段，避免将旧版本或不同标签格式误报为更新。
func newerVersion(latest, current string) bool {
	parse := func(v string) ([]int, bool) {
		parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
		if len(parts) != 3 {
			return nil, false
		}
		values := make([]int, 3)
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return nil, false
			}
			values[i] = n
		}
		return values, true
	}
	a, ok := parse(latest)
	if !ok {
		return false
	}
	b, ok := parse(current)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// 每次開啟頁面都會查詢版本;GitHub 未驗證介面每 IP 每小時僅 60 次,
// 成功結果快取 1 小時,失敗結果快取 5 分鐘,避免多分頁/頻繁重新整理被限流。
const (
	versionCacheTTL     = time.Hour
	versionFailCacheTTL = 5 * time.Minute
)

type latestRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

type versionCache struct {
	mu        sync.Mutex
	release   *latestRelease
	checkedAt time.Time
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"current_version": Version, "has_update": false, "check_failed": true}
	if rel := s.latestRelease(r.Context()); rel != nil {
		out["latest_version"] = rel.TagName
		out["has_update"] = newerVersion(rel.TagName, Version)
		out["html_url"] = rel.HTMLURL
		out["check_failed"] = false
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) latestRelease(ctx context.Context) *latestRelease {
	c := &s.version
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := versionCacheTTL
	if c.release == nil {
		ttl = versionFailCacheTTL
	}
	if !c.checkedAt.IsZero() && time.Since(c.checkedAt) < ttl {
		return c.release
	}
	// 結果供所有請求共用,不隨單一瀏覽器請求取消而記成失敗。
	c.release, c.checkedAt = fetchLatestRelease(context.WithoutCancel(ctx)), time.Now()
	return c.release
}

func fetchLatestRelease(ctx context.Context) *latestRelease {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/27Aaron/WeRead-Kit/releases/latest", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var rel latestRelease
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&rel) != nil || rel.TagName == "" {
		return nil
	}
	return &rel
}
