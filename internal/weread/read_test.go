package weread

import "testing"

// 期望值来自参考实现的 Python 原始代码(wereadapi tests/weread_api_test.py)直接计算,
// 本测试保证 Go 移植与 Python 逐字节一致——签名错一个字符服务端就会拒绝上报。
const webUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func TestCalcHashMatchesReference(t *testing.T) {
	cases := map[string]string{
		"3300060341":                     "81032840813ab7e12g0111d4",
		"ce032b305a9bc1ce0b0dd2a":        "a5b42b72e63653033326233303561396263316365306230646432613eb",
		"0":                              "cfc32da010cfcd208495488",
		"123456789012345678901234567890": "a46321d0775bcd15g06bc614eg0835b7bf87g0337aa1c",
	}
	for input, want := range cases {
		if got := calcHash(input); got != want {
			t.Errorf("calcHash(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetWebAppIDMatchesReference(t *testing.T) {
	want := "wb182564874663h1256619504"
	if got := getWebAppID(webUA); got != want {
		t.Errorf("getWebAppID = %q, want %q", got, want)
	}
}

func TestSignPayloadMatchesReference(t *testing.T) {
	payload := map[string]any{
		"appId": getWebAppID(webUA),
		"b":     calcHash("3300060341"),
		"c":     calcHash("1"),
		"ci":    1,
		"co":    389,
		"ct":    1744264311,
		"dy":    0,
		"fm":    "epub",
		"pc":    calcHash("0"),
		"pr":    74,
		"ps":    calcHash("0"),
		"sm":    "",
		"rt":    30,
		"ts":    1744264311434,
		"rn":    466,
	}
	if got := signPayload(payload); got != "7406cc1f" {
		t.Errorf("signPayload = %q, want %q", got, "7406cc1f")
	}
}
