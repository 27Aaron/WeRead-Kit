package weread

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// readerTestCalls 统计各接口调用次数,并模拟两点线上实测行为:
//
//   - 桥接接口 /web/login/session/init 不校验凭据新鲜度:任何 skey 都返回 200 并下发 wr_skey;
//     但只有 skey 为 fresh-access 时下发的 Cookie 才真正可用,其余都是「死 Cookie」。
//   - /web/book/read 只在拿到可用 Cookie 时才接受请求:阅读会话初始化返回 readerToken,
//     心跳返回 succ/synckey;死 Cookie 一律 HTTP 401。
type readerTestCalls struct {
	bridge   atomic.Int32
	renewal  atomic.Int32
	refresh  atomic.Int32
	readInit atomic.Int32
	beat     atomic.Int32
	chapters atomic.Int32
}

func (n *readerTestCalls) transport(t *testing.T) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		ok := func(body string, hdr http.Header) *http.Response {
			if hdr == nil {
				hdr = make(http.Header)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     hdr,
			}
		}
		unauthorized := func() *http.Response {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}
		}
		alive := func(r *http.Request) bool {
			return strings.Contains(r.Header.Get("cookie"), "wr_skey=good")
		}

		switch r.URL.Path {
		case "/web/login/session/init":
			// 关键:无论凭据是否有效都返回 200 + wr_skey,死 Cookie 正来源于此。
			n.bridge.Add(1)
			raw, _ := io.ReadAll(r.Body)
			var payload struct {
				Skey string `json:"skey"`
			}
			_ = json.Unmarshal(raw, &payload)
			skey := "dead"
			if payload.Skey == "fresh-access" {
				skey = "good"
			}
			return ok(`{"success":1}`, http.Header{
				"Set-Cookie": []string{"wr_vid=v1; Path=/", "wr_skey=" + skey + "; Path=/"},
			}), nil
		case "/web/login/renewal":
			// 续用现有 Cookie 救不回来。
			n.renewal.Add(1)
			return unauthorized(), nil
		case "/login":
			// 移动端刷新接口:正常轮换出可用凭据。
			n.refresh.Add(1)
			return ok(`{"vid":"v1","accessToken":"fresh-access","refreshToken":"fresh-refresh"}`, nil), nil
		case "/web/book/chapterInfos":
			n.chapters.Add(1)
			return ok(`{}`, nil), nil
		case "/web/book/read":
			if !alive(r) {
				return unauthorized(), nil
			}
			raw, _ := io.ReadAll(r.Body)
			// rt 是心跳专有字段(readPayload 不含),据此区分初始化与心跳。
			if strings.Contains(string(raw), `"rt"`) {
				n.beat.Add(1)
				return ok(`{"succ":1,"synckey":"synckey"}`, nil), nil
			}
			n.readInit.Add(1)
			return ok(`{"readerToken":"reader-token"}`, nil), nil
		}
		t.Fatalf("未预期的请求路径: %s", r.URL.Path)
		return unauthorized(), nil
	}
}

// TestFarmSessionRecoversFromExpiredCredentialsAtStartup 复现「启动瞬间凭据已失效」:
// 移动端凭据在会话启动时已经过期(预热失败或隔夜失效时会真实发生),而桥接接口对过期凭据
// 同样返回成功,于是第一级续期只能拿到死 Cookie。修复前 FarmSession 会立即以
// 「会话已过期」结束;修复后应刷新凭据、重新桥接并正常读完。
func TestFarmSessionRecoversFromExpiredCredentialsAtStartup(t *testing.T) {
	var calls readerTestCalls
	client := NewClient()
	client.HTTP.Transport = calls.transport(t)

	creds := &Credentials{Vid: "v1", AccessToken: "stale-access", RefreshToken: "refresh-token", DeviceID: "device"}
	task := FarmTask{BookID: "book", Done: 0, Total: 1}

	result, next, err := client.FarmSession(context.Background(), creds, task, nil, NewFarmControl())
	if err != nil {
		t.Fatalf("FarmSession 返回错误: %v", err)
	}
	if result.Err != "" {
		t.Fatalf("result.Err = %q, want 空(启动阶段应能自愈)", result.Err)
	}
	if result.Done != 1 {
		t.Fatalf("result.Done = %d, want 1", result.Done)
	}
	if calls.refresh.Load() == 0 {
		t.Fatal("未调用 Refresh:启动路径缺少第二级恢复")
	}
	if next == nil || next.AccessToken != "fresh-access" {
		t.Fatalf("凭据未轮换: %#v", next)
	}
}

// TestFarmSessionKeepsCredentialsWhenSessionIsAlive 保证恢复逻辑只在必要时介入:
// 凭据有效时不应触发任何续期,避免无谓的 refreshToken 轮换。
func TestFarmSessionKeepsCredentialsWhenSessionIsAlive(t *testing.T) {
	var calls readerTestCalls
	client := NewClient()
	client.HTTP.Transport = calls.transport(t)

	creds := &Credentials{Vid: "v1", AccessToken: "fresh-access", RefreshToken: "refresh-token", DeviceID: "device"}
	task := FarmTask{BookID: "book", Done: 0, Total: 1}

	result, next, err := client.FarmSession(context.Background(), creds, task, nil, NewFarmControl())
	if err != nil {
		t.Fatalf("FarmSession 返回错误: %v", err)
	}
	if result.Err != "" || result.Done != 1 {
		t.Fatalf("result = %#v, want Done=1 且无错误", result)
	}
	if got := calls.renewal.Load() + calls.refresh.Load(); got != 0 {
		t.Fatalf("凭据有效时不应触发续期: renewal=%d refresh=%d", calls.renewal.Load(), calls.refresh.Load())
	}
	if next == nil || next.AccessToken != "fresh-access" {
		t.Fatalf("凭据不应被改动: %#v", next)
	}
}
