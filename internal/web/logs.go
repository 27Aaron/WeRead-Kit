// 日志查询 API 与 Server 级别的统一日志入口:写 SQLite(供日志页查看)+ 打到进程 stdout。
package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/27Aaron/weread-kit/internal/store"
)

// logf 是 Server 内所有组件写日志的统一入口:同时落库与打 stdout。
func (s *Server) logf(level, source, vid, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	store.AddLog(s.db, level, source, vid, message)
	log.Printf("[%s]%s %s", source, aliasPrefix(vid), message)
}

func aliasPrefix(vid string) string {
	if vid == "" {
		return ""
	}
	return " " + vid
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		if err := store.ClearLogs(s.db); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		logs, err := store.ListLogs(s.db, r.URL.Query().Get("vid"), r.URL.Query().Get("level"), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, logs)
		return
	default:
		writeErr(w, http.StatusMethodNotAllowed, errors.New("不支持的方法"))
	}
}
