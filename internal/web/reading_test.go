package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

func TestStartFarmStopsWhenInitialStateCannotBeSaved(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()

	s := &Server{db: db, ctx: context.Background(), farms: map[string]*farmSessionHandle{}}
	err = s.startFarm("v1", weread.FarmTask{BookID: "book", Total: 2}, &weread.Credentials{Vid: "v1"})
	if err == nil {
		t.Fatal("startFarm succeeded despite state persistence failure")
	}
	if s.farmRunning("v1") {
		t.Fatal("failed task remained marked as running")
	}
}

// TestReadingConfigValidation 验证 RunAt 补零校验、书 ID 分隔符拒绝与自动去重。
func TestReadingConfigValidation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{db: db, farms: map[string]*farmSessionHandle{}}

	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/v1/reading", strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.SetPathValue("vid", "v1")
		rec := httptest.NewRecorder()
		s.handleReadingConfig(rec, req)
		return rec.Code
	}

	if post(`{"enabled":true,"book_ids":["b1"],"minutes":30,"run_at":"3:00"}`) != http.StatusBadRequest {
		t.Fatal("non-padded run_at: want 400")
	}
	if post(`{"enabled":true,"book_ids":["a,b"],"minutes":30,"run_at":"03:00"}`) != http.StatusBadRequest {
		t.Fatal("book id containing comma: want 400")
	}
	if post(`{"enabled":true,"book_ids":["b1","b1","b2"],"minutes":30,"run_at":"03:00"}`) != http.StatusOK {
		t.Fatal("valid config: want 200")
	}
	cfg, err := store.GetReadingConfig(db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if want := []string{"b1", "b2"}; !reflect.DeepEqual(cfg.BookIDs, want) {
		t.Fatalf("book_ids = %v, want %v", cfg.BookIDs, want)
	}
	// 空书单合法:留空表示「每天从书架随机挑一本」,保存后再读回验证。
	if post(`{"enabled":true,"book_ids":[],"minutes":30,"run_at":"03:00"}`) != http.StatusOK {
		t.Fatal("empty book_ids: want 200")
	}
	cfg, err = store.GetReadingConfig(db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if len(cfg.BookIDs) != 0 {
		t.Fatalf("empty book_ids saved as %v, want empty", cfg.BookIDs)
	}
}

// newReadingServer 构造直调调度器的最小 Server:临时库 + 桩化官方接口,
// 模式与 newChallengeServer 一致,另带 tickFarm/startFarm 必需的 ctx 与 farms。
func newReadingServer(t *testing.T, transport stubTransport) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev", AccessToken: "tok"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	client := weread.NewClient()
	client.HTTP.Transport = transport
	return &Server{db: db, client: client, ctx: context.Background(), farms: map[string]*farmSessionHandle{}}
}

// weeklyStubBody 生成 weekReadDaysDetail 桩响应:今天(按运行日历)记 seconds 秒。
func weeklyStubBody(t *testing.T, seconds int) string {
	t.Helper()
	days := make([]int, 7)
	days[(int(time.Now().Weekday())+6)%7] = seconds
	b, err := json.Marshal(map[string]any{"weekReadDaysDetail": map[string]any{"readDays": days}})
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	return string(b)
}

// TestReadingSkipIfReadSetting 校验「当天已读过就跳过」开关:未存过按默认开启
// (issue #134),POST 显式传入才覆盖,不带该字段的旧客户端不改动已存值。
func TestReadingSkipIfReadSetting(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := store.Save(db, &store.Credential{Vid: "v1", RefreshToken: "rt", DeviceID: "dev"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{db: db, farms: map[string]*farmSessionHandle{}}

	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/v1/reading", strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		req.SetPathValue("vid", "v1")
		rec := httptest.NewRecorder()
		s.handleReadingConfig(rec, req)
		return rec.Code
	}
	get := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/api/accounts/v1/reading", nil)
		req.SetPathValue("vid", "v1")
		rec := httptest.NewRecorder()
		s.handleReadingConfig(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d", rec.Code)
		}
		var out struct {
			SkipIfRead bool `json:"skip_if_read"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.SkipIfRead
	}

	if !get() {
		t.Fatal("fresh config: skip_if_read should default to true")
	}
	if post(`{"enabled":true,"book_ids":["b1"],"minutes":30,"run_at":"03:00","skip_if_read":false}`) != http.StatusOK {
		t.Fatal("save skip=false: want 200")
	}
	if get() {
		t.Fatal("skip_if_read should be false after save")
	}
	if post(`{"enabled":true,"book_ids":["b1"],"minutes":30,"run_at":"03:00"}`) != http.StatusOK {
		t.Fatal("save without field: want 200")
	}
	if get() {
		t.Fatal("save without skip_if_read must not touch stored value")
	}
	if post(`{"enabled":true,"book_ids":["b1"],"minutes":30,"run_at":"03:00","skip_if_read":true}`) != http.StatusOK {
		t.Fatal("save skip=true: want 200")
	}
	if !get() {
		t.Fatal("skip_if_read should be true after re-save")
	}
}

// TestTickFarmSkipsWhenAlreadyRead 校验当日已读判定:今天已记 10 分钟(超过
// 1 分钟门槛)时到点跳过、标记当日已处理,不再启动会话(issue #134)。
func TestTickFarmSkipsWhenAlreadyRead(t *testing.T) {
	weeklyCalls := 0
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/weekly/exchange" {
			t.Errorf("unexpected upstream call: %s", r.URL.Path)
			return stubResp(http.StatusInternalServerError, "{}"), nil
		}
		weeklyCalls++
		return stubResp(http.StatusOK, weeklyStubBody(t, 600)), nil
	}))
	if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{Vid: "v1", Enabled: true, BookIDs: []string{"b1"}, Minutes: 30, RunAt: "00:00"}); err != nil {
		t.Fatalf("SaveReadingConfig: %v", err)
	}
	s.tickFarm(context.Background())
	if weeklyCalls != 1 {
		t.Fatalf("weekly/exchange calls = %d, want 1", weeklyCalls)
	}
	cfg, err := store.GetReadingConfig(s.db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if cfg.LastRunDate != time.Now().Format("2006-01-02") || !strings.Contains(cfg.LastStatus, "跳过自动阅读") {
		t.Fatalf("run state = %s %q, want today marked with skip status", cfg.LastRunDate, cfg.LastStatus)
	}
	if s.farmRunning("v1") {
		t.Fatal("farm should not start when already read")
	}
}

// TestTickFarmBelowThresholdKeepsReading 校验判定门槛:今天只记 30 秒(<1 分钟,
// 与官方「当日阅读 ≥1 分钟记 1 天」口径一致)时视为没读,照常启动会话补读。
func TestTickFarmBelowThresholdKeepsReading(t *testing.T) {
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/weekly/exchange" {
			return stubResp(http.StatusOK, weeklyStubBody(t, 30)), nil
		}
		// 会话链路(桥接/心跳)全部拒绝:断言只关心会话已启动,后续失败无关紧要。
		return stubResp(http.StatusInternalServerError, "{}"), nil
	}))
	if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{Vid: "v1", Enabled: true, BookIDs: []string{"b1"}, Minutes: 30, RunAt: "00:00"}); err != nil {
		t.Fatalf("SaveReadingConfig: %v", err)
	}
	s.tickFarm(context.Background())
	cfg, err := store.GetReadingConfig(s.db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if cfg.LastRunDate != time.Now().Format("2006-01-02") || !strings.Contains(cfg.LastStatus, "正在阅读") {
		t.Fatalf("run state = %s %q, want farm started today", cfg.LastRunDate, cfg.LastStatus)
	}
}

// TestTickFarmSkipDisabledRunsAnyway 校验开关关闭时保持旧行为:即使当日已读也照常执行。
func TestTickFarmSkipDisabledRunsAnyway(t *testing.T) {
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/weekly/exchange" {
			return stubResp(http.StatusOK, weeklyStubBody(t, 600)), nil
		}
		return stubResp(http.StatusInternalServerError, "{}"), nil
	}))
	if err := store.SetSetting(s.db, readingSkipIfReadKey+"v1", "0"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{Vid: "v1", Enabled: true, BookIDs: []string{"b1"}, Minutes: 30, RunAt: "00:00"}); err != nil {
		t.Fatalf("SaveReadingConfig: %v", err)
	}
	s.tickFarm(context.Background())
	cfg, err := store.GetReadingConfig(s.db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if !strings.Contains(cfg.LastStatus, "正在阅读") {
		t.Fatalf("status = %q, want farm started with skip disabled", cfg.LastStatus)
	}
}

// shelfStubBody 生成书架桩响应:两本可读书目。
const shelfStubBody = `{"books":[{"bookId":"sb1","title":"书一"},{"bookId":"sb2","title":"书二"}]}`

// emptyBookListConfig 存一份启用了调度但未选书单的配置。
func emptyBookListConfig(t *testing.T, s *Server) {
	t.Helper()
	if err := store.SaveReadingConfig(s.db, &store.ReadingConfig{Vid: "v1", Enabled: true, BookIDs: []string{}, Minutes: 30, RunAt: "00:00"}); err != nil {
		t.Fatalf("SaveReadingConfig: %v", err)
	}
}

// TestTickFarmEmptyBookListPicksFromShelf 校验未选书单时从书架随机选一本:
// 到点实时拉一次书架并正常启动会话。
func TestTickFarmEmptyBookListPicksFromShelf(t *testing.T) {
	shelfCalls := 0
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/weekly/exchange":
			// 今天没读过(0 秒,低于门槛):照常进入调度。
			return stubResp(http.StatusOK, weeklyStubBody(t, 0)), nil
		case "/shelf/sync":
			shelfCalls++
			return stubResp(http.StatusOK, shelfStubBody), nil
		default:
			return stubResp(http.StatusInternalServerError, "{}"), nil
		}
	}))
	emptyBookListConfig(t, s)
	s.tickFarm(context.Background())
	if shelfCalls != 1 {
		t.Fatalf("shelf/sync calls = %d, want 1", shelfCalls)
	}
	cfg, err := store.GetReadingConfig(s.db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if cfg.LastRunDate != time.Now().Format("2006-01-02") || !strings.Contains(cfg.LastStatus, "正在阅读") {
		t.Fatalf("run state = %s %q, want farm started with a shelf book", cfg.LastRunDate, cfg.LastStatus)
	}
}

// TestTickFarmEmptyBookListShelfFailureSkipsDay 校验书架获取失败时的兜底:
// 记下当日已处理并跳过(持续状态,避免每 30 秒重复打失败请求),次轮不再拉书架。
func TestTickFarmEmptyBookListShelfFailureSkipsDay(t *testing.T) {
	shelfCalls := 0
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/weekly/exchange":
			return stubResp(http.StatusOK, weeklyStubBody(t, 0)), nil
		case "/shelf/sync":
			shelfCalls++
			return stubResp(http.StatusInternalServerError, "{}"), nil
		default:
			return stubResp(http.StatusInternalServerError, "{}"), nil
		}
	}))
	emptyBookListConfig(t, s)
	s.tickFarm(context.Background())
	s.tickFarm(context.Background())
	if shelfCalls != 1 {
		t.Fatalf("shelf/sync calls = %d, want 1 (day marked after first failure)", shelfCalls)
	}
	cfg, err := store.GetReadingConfig(s.db, "v1")
	if err != nil {
		t.Fatalf("GetReadingConfig: %v", err)
	}
	if !strings.Contains(cfg.LastStatus, "书架获取失败") {
		t.Fatalf("status = %q, want shelf failure noted", cfg.LastStatus)
	}
	if s.farmRunning("v1") {
		t.Fatal("farm should not start when shelf fetch fails")
	}
}

// TestReadingRunEmptyBookListUsesShelf 校验「立即执行」在未选书单时实时拉书架随机选一本。
func TestReadingRunEmptyBookListUsesShelf(t *testing.T) {
	s := newReadingServer(t, stubTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/shelf/sync" {
			return stubResp(http.StatusOK, shelfStubBody), nil
		}
		return stubResp(http.StatusInternalServerError, "{}"), nil
	}))
	emptyBookListConfig(t, s)
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/v1/reading/run", nil)
	req.SetPathValue("vid", "v1")
	rec := httptest.NewRecorder()
	s.handleReadingRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		Started bool `json:"started"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Started {
		t.Fatalf("body = %s, want started=true", rec.Body.String())
	}
}
