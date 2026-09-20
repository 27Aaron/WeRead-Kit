package weread

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCookieUpdatesAreIsolatedAndHonorDeletion(t *testing.T) {
	updates := []*http.Cookie{{Name: "wr_skey", Value: "new", Domain: ".weread.qq.com", Path: "/", Secure: true}}
	if got := mergeWebCookies("wr_vid=a; wr_skey=old", updates); got != "wr_vid=a; wr_skey=new" {
		t.Fatal(got)
	}
	if got := mergeWebCookies("wr_vid=b; wr_skey=other", nil); got != "wr_vid=b; wr_skey=other" {
		t.Fatal(got)
	}
	for _, ck := range []*http.Cookie{
		{Name: "wr_skey", Path: "/", MaxAge: -1},
		{Name: "wr_skey", Path: "/", Expires: time.Now().Add(-time.Hour)},
	} {
		if got := mergeWebCookies("wr_vid=a; wr_skey=old", []*http.Cookie{ck}); got != "wr_vid=a" {
			t.Fatal(got)
		}
	}
	if got := mergeWebCookies("wr_skey=old", []*http.Cookie{{Name: "wr_skey", Value: "wrong", Domain: "example.com"}}); got != "wr_skey=old" {
		t.Fatal(got)
	}
}

func TestRenewRejectsInvalidSuccess(t *testing.T) {
	for _, body := range []string{`{}`, `{"succ":0}`, `null`, `<html>error</html>`} {
		t.Run(body, func(t *testing.T) {
			c := NewClient()
			c.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			if _, err := c.RenewWebCookie(context.Background(), "wr_skey=old"); err == nil {
				t.Fatal("accepted invalid renewal")
			}
		})
	}
}

func TestReadRecoveryRenewsAndReinitializes(t *testing.T) {
	c := NewClient()
	calls := 0
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, status := "", 200
		h := make(http.Header)
		switch calls {
		case 1:
			status = 401
			body = `{}`
		case 2:
			if r.URL.Path != "/web/login/renewal" {
				t.Fatal(r.URL.Path)
			}
			h.Add("Set-Cookie", "wr_skey=new; Path=/")
			body = `{"succ":1}`
		case 3:
			if r.Header.Get("Cookie") != "wr_skey=new" {
				t.Fatal("stale cookie")
			}
			body = `{"readerToken":"new-token"}`
		case 4:
			if r.Header.Get("Cookie") != "wr_skey=new" {
				t.Fatal("stale cookie")
			}
			body = `{"succ":1,"synckey":1}`
		default:
			t.Fatal("unexpected extra request")
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	creds := &Credentials{Vid: "a"}
	ok, cookie, active, reader, err := c.readWithRecovery(context.Background(), "wr_skey=old", creds, &ReaderSession{Token: "old"}, "book", 1, 0, 0)
	if err != nil || !ok || cookie != "wr_skey=new" || active != creds || reader.Token != "new-token" || calls != 4 {
		t.Fatalf("recovery failed: ok=%v calls=%d err=%v", ok, calls, err)
	}
}
