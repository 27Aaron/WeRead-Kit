package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const Version = "0.1.5"

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

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"current_version": Version, "has_update": false, "check_failed": true}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/27Aaron/wxread/releases/latest", nil)
	if err != nil {
		writeJSON(w, 200, out)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		var rel struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
		}
		if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&rel) == nil && rel.TagName != "" {
			out["latest_version"] = rel.TagName
			out["has_update"] = newerVersion(rel.TagName, Version)
			out["html_url"] = rel.HTMLURL
			out["check_failed"] = false
		}
	}
	writeJSON(w, 200, out)
}
