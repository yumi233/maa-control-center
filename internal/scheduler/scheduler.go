package scheduler

import (
	"fmt"
	"time"
	_ "time/tzdata"
)

// Next returns a strictly future occurrence, in the explicitly chosen time zone.
func Next(after time.Time, hour, minute int, weekdays []int, zone string) (time.Time, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || len(weekdays) == 0 || len(weekdays) > 7 || zone != "Asia/Shanghai" {
		return time.Time{}, fmt.Errorf("请选择北京时间、有效时间和至少一个星期")
	}
	days := map[int]bool{}
	for _, d := range weekdays {
		if d < 0 || d > 6 || days[d] {
			return time.Time{}, fmt.Errorf("星期无效或重复")
		}
		days[d] = true
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return time.Time{}, e
	}
	a := after.In(loc)
	for i := 0; i < 8; i++ {
		day := a.AddDate(0, 0, i)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		if days[int(candidate.Weekday())] && candidate.After(after) {
			return candidate.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("无法计算下次运行时间")
}
