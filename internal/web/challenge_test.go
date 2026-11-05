package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/27Aaron/weread-kit/internal/store"
	"github.com/27Aaron/weread-kit/internal/weread"
)

// stubTransport 让 web 层测试能把官方接口桩在 RoundTripper 上,
// 模式与 internal/weread 的单测一致。
type stubTransport func(*http.Request) (*http.Response, error)

func (f stubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// newChallengeServer 构造直调 handler 的最小 Server:临时库 + 桩化的官方接口。
func newChallengeServer(t *testing.T, transport stubTransport) *Server {
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
	return &Server{db: db, client: client}
}

func postJoin(s *Server, vid, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+vid+"/challenge/join", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.SetPathValue("vid", vid)
	rec := httptest.NewRecorder()
	s.handleChallengeJoin(rec, req)
	return rec
}

// TestChallengeJoinValidation 覆盖请求体非 JSON 与白名单外的 challenge_id。
func TestChallengeJoinValidation(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("校验失败的请求不应触达官方接口")
		return nil, nil
	})
	if rec := postJoin(s, "v1", `not-json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("非 JSON body: status = %d, want 400", rec.Code)
	}
	if rec := postJoin(s, "v1", `{"challenge_id":"readchallenge4"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("付费挑战 ID: status = %d, want 400", rec.Code)
	}
}

// TestChallengeJoinUnknownAccount 未知账号返回 404。
func TestChallengeJoinUnknownAccount(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("未知账号不应触达官方接口")
		return nil, nil
	})
	if rec := postJoin(s, "ghost", `{"challenge_id":"ChallengeLongTermFree"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("未知账号: status = %d, want 404", rec.Code)
	}
}

// TestChallengeJoinAlreadyJoined 映射为 409,前端据此提示"已参与"。
func TestChallengeJoinAlreadyJoined(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		return stubResp(http.StatusOK, `{"errcode":-2904,"errmsg":"-2904"}`), nil
	})
	if rec := postJoin(s, "v1", `{"challenge_id":"ChallengeLongTermFree"}`); rec.Code != http.StatusConflict {
		t.Fatalf("已参与: status = %d, want 409", rec.Code)
	}
}

// TestChallengeJoinSuccess 报名成功返回 200 并落一条审计日志。
func TestChallengeJoinSuccess(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/challenge/join" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		return stubResp(http.StatusOK, `{"succ":1}`), nil
	})
	rec := postJoin(s, "v1", `{"challenge_id":"ChallengeLongTermFree"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var msg string
	if err := s.db.QueryRow(`SELECT message FROM weread_log WHERE vid = 'v1' ORDER BY id DESC LIMIT 1`).Scan(&msg); err != nil {
		t.Fatalf("查询审计日志: %v", err)
	}
	if !strings.Contains(msg, "21 天阅读挑战") {
		t.Fatalf("审计日志 = %q, want 报名记录", msg)
	}
}

// --- 自动参与:设置 API ---

func postAutoJoin(s *Server, vid, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+vid+"/challenge/auto-join", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.SetPathValue("vid", vid)
	rec := httptest.NewRecorder()
	s.handleChallengeAutoJoin(rec, req)
	return rec
}

func TestChallengeAutoJoinSetting(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("开关保存不应触达官方接口")
		return nil, nil
	})
	if rec := postAutoJoin(s, "v1", `not-json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("非 JSON: status = %d, want 400", rec.Code)
	}
	if rec := postAutoJoin(s, "ghost", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("未知账号: status = %d, want 404", rec.Code)
	}
	if rec := postAutoJoin(s, "v1", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("保存开关: status = %d, want 200", rec.Code)
	}
	if !s.challengeAutoJoinOn("v1") {
		t.Fatal("开关未落库")
	}
}

// TestChallengeDetailIncludesAutoJoin GET 响应携带开关,前端据此回显。
func TestChallengeDetailIncludesAutoJoin(t *testing.T) {
	s := newChallengeServer(t, func(r *http.Request) (*http.Response, error) {
		return stubResp(http.StatusOK, `{"challengeList":[]}`), nil
	})
	if err := store.SetSetting(s.db, challengeAutoJoinKey+"v1", "1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/v1/challenge", nil)
	req.SetPathValue("vid", "v1")
	rec := httptest.NewRecorder()
	s.handleChallengeDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"auto_join":true`) {
		t.Fatalf("响应缺少 auto_join: %s", rec.Body.String())
	}
}

// --- 自动参与:调度器 ---

// schedStub 构造带请求计数的官方接口桩:detail/join 各自返回指定内容。
type schedStub struct {
	detailStatus int
	detailBody   string
	joinStatus   int
	joinBody     string
	detailCalls  int
	joinCalls    int
}

func (st *schedStub) transport() stubTransport {
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/challenge/detail":
			st.detailCalls++
			return stubResp(st.detailStatus, st.detailBody), nil
		case "/challenge/join":
			st.joinCalls++
			return stubResp(st.joinStatus, st.joinBody), nil
		}
		return stubResp(http.StatusNotFound, `{}`), nil
	}
}

func enableAutoJoin(t *testing.T, s *Server, vid string) {
	t.Helper()
	if err := store.SetSetting(s.db, challengeAutoJoinKey+vid, "1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

func TestAutoJoinJoinsWhenNoActiveChallenge(t *testing.T) {
	st := &schedStub{detailStatus: http.StatusOK, detailBody: `{"challengeList":[]}`, joinStatus: http.StatusOK, joinBody: `{"succ":1}`}
	s := newChallengeServer(t, st.transport())
	enableAutoJoin(t, s, "v1")

	s.autoJoinChallenges(context.Background())
	if st.detailCalls != 1 || st.joinCalls != 1 {
		t.Fatalf("detail=%d join=%d, want 1/1", st.detailCalls, st.joinCalls)
	}
}

func TestAutoJoinJoinsAfterChallengeEnds(t *testing.T) {
	// 已结束的挑战(status=2)也应触发报名下一期,并记录完成通知去重键。
	st := &schedStub{
		detailStatus: http.StatusOK,
		detailBody:   `{"challengeList":[{"id":"ChallengeLongTermFree","status":2,"startTime":100,"endTime":200,"readDays":21}]}`,
		joinStatus:   http.StatusOK,
		joinBody:     `{"succ":1}`,
	}
	s := newChallengeServer(t, st.transport())
	enableAutoJoin(t, s, "v1")

	s.autoJoinChallenges(context.Background())
	if st.joinCalls != 1 {
		t.Fatalf("join=%d, want 1", st.joinCalls)
	}
	if v, err := store.GetSetting(s.db, challengeNotifiedStartKey+"v1"); err != nil || v != "100" {
		t.Fatalf("完成通知去重键 = %q err=%v, want 100", v, err)
	}
}

func TestAutoJoinSkipsWhenInProgress(t *testing.T) {
	st := &schedStub{
		detailStatus: http.StatusOK,
		detailBody:   `{"challengeList":[{"id":"ChallengeLongTermFree","status":1,"startTime":100}]}`,
	}
	s := newChallengeServer(t, st.transport())
	enableAutoJoin(t, s, "v1")

	s.autoJoinChallenges(context.Background())
	if st.joinCalls != 0 {
		t.Fatalf("进行中不应报名, join=%d", st.joinCalls)
	}
}

func TestAutoJoinRespectsSwitchAndBackoff(t *testing.T) {
	st := &schedStub{detailStatus: http.StatusOK, detailBody: `{"challengeList":[]}`}
	s := newChallengeServer(t, st.transport())

	// 开关关闭:不发起任何请求。
	s.autoJoinChallenges(context.Background())
	if st.detailCalls != 0 {
		t.Fatalf("开关关闭仍查询, detail=%d", st.detailCalls)
	}

	// 开启后查询失败:进入退避,下一轮不再请求。
	st.detailStatus = http.StatusInternalServerError
	enableAutoJoin(t, s, "v1")
	s.autoJoinChallenges(context.Background())
	if st.detailCalls != 1 {
		t.Fatalf("detail=%d, want 1", st.detailCalls)
	}
	s.autoJoinChallenges(context.Background())
	if st.detailCalls != 1 {
		t.Fatalf("退避期内不应重试, detail=%d", st.detailCalls)
	}
}

func TestAutoJoinTreatsAlreadyJoinedAsDone(t *testing.T) {
	// 视图竞态:detail 显示未报名但 join 返回 -2904,应静默吸收,不进入退避。
	st := &schedStub{detailStatus: http.StatusOK, detailBody: `{"challengeList":[]}`, joinStatus: http.StatusOK, joinBody: `{"errcode":-2904,"errmsg":"-2904"}`}
	s := newChallengeServer(t, st.transport())
	enableAutoJoin(t, s, "v1")

	s.autoJoinChallenges(context.Background())
	if st.joinCalls != 1 {
		t.Fatalf("join=%d, want 1", st.joinCalls)
	}
	if !s.challengeBackoffUntil("v1").IsZero() {
		t.Fatal("-2904 不应触发退避")
	}
}
