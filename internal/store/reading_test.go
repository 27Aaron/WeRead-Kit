package store

import (
	"reflect"
	"testing"
)

func TestSplitBookIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a,b , c,,a", []string{"a", "b", "c"}},
		{`["a","b"]`, []string{"a", "b"}},
		{"[]", []string{}},
		// 以 [ 开头但不是合法 JSON 时回落逗号分隔,不丢 ID。
		{"[bad", []string{"[bad"}},
	}
	for _, c := range cases {
		if got := splitBookIDs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitBookIDs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
