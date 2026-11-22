package weread

import (
	"testing"
	"time"
)

// TestWeeklyRewardsTodayReadSeconds 固定用 2026-11-16(周一)..2026-11-22(周日)这一周,
// 避免依赖运行日期;取值口径见 TodayReadSeconds。
func TestWeeklyRewardsTodayReadSeconds(t *testing.T) {
	week := []int{60, 120, 0, 300, 30, 0, 600} // 周一..周日
	twoWeeks := append([]int{1, 2, 3, 4, 5, 6, 7}, week...)
	day := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }

	cases := []struct {
		name string
		rw   WeeklyRewards
		now  time.Time
		want int
		ok   bool
	}{
		{"周一", WeeklyRewards{WeekReadDaysDetail: WeeklyPeriodDetail{ReadDays: week}}, day(16), 60, true},
		{"周三无阅读", WeeklyRewards{WeekReadDaysDetail: WeeklyPeriodDetail{ReadDays: week}}, day(18), 0, true},
		{"周日", WeeklyRewards{WeekReadDaysDetail: WeeklyPeriodDetail{ReadDays: week}}, day(22), 600, true},
		{"超长取最后7项", WeeklyRewards{WeekReadDaysDetail: WeeklyPeriodDetail{ReadDays: twoWeeks}}, day(22), 600, true},
		{"明细缺失", WeeklyRewards{}, day(18), 0, false},
		{"今天不在明细内", WeeklyRewards{WeekReadDaysDetail: WeeklyPeriodDetail{ReadDays: week[:6]}}, day(22), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.rw.TodayReadSeconds(c.now)
			if got != c.want || ok != c.ok {
				t.Fatalf("TodayReadSeconds(%v) = (%d, %v), want (%d, %v)", c.now, got, ok, c.want, c.ok)
			}
		})
	}
}
