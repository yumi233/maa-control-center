package scheduler

import (
	"testing"
	"time"
)

func TestWeeklyBoundaryAndValidation(t *testing.T) {
	at := time.Date(2026, 10, 25, 0, 0, 0, 0, time.FixedZone("UTC8", 8*3600))
	next, e := Next(at, 0, 0, []int{0}, "Asia/Shanghai")
	if e != nil || next.Sub(at) != 7*24*time.Hour {
		t.Fatalf("same occurrence replayed %v %v", next, e)
	}
	next, e = Next(at.Add(-time.Second), 0, 0, []int{0}, "Asia/Shanghai")
	if e != nil || !next.Equal(at) {
		t.Fatal("midnight boundary missed")
	}
	for _, days := range [][]int{nil, {7}, {1, 1}} {
		if _, e := Next(at, 9, 0, days, "Asia/Shanghai"); e == nil {
			t.Fatal("invalid weekdays accepted")
		}
	}
	if _, e := Next(at, 24, 0, []int{1}, "Asia/Shanghai"); e == nil {
		t.Fatal("invalid hour")
	}
	if _, e := Next(at, 9, 0, []int{1}, "UTC"); e == nil {
		t.Fatal("unconfigured timezone")
	}
}
