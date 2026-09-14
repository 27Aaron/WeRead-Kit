package web

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.1.3", "0.1.2", true},
		{"v0.1.2", "0.1.2", false},
		{"v0.1.1", "0.1.2", false},
		{"v0.2.0", "0.1.9", true},
		{"v1.0.0", "0.9.9", true},
		{"v0.1.10", "0.1.9", true},      // 数值比较,不是字典序
		{"v0.1.3-beta", "0.1.2", false}, // 非稳定标签不提示更新
		{"", "0.1.2", false},
		{"v0.1", "0.1.2", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
