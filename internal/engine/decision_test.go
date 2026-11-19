package engine

import (
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"testing"
	"time"
)

func TestDesired(t *testing.T) {
	c := api.Scaling{MinReplicas: 2, MaxReplicas: 10, MaxStep: 3}
	for _, tc := range []struct {
		name                            string
		current, requested, floor, want int32
	}{
		{"zero never shrinks", 6, 0, 0, 6}, {"KEDA demand", 2, 4, 0, 4}, {"step", 2, 10000, 0, 5},
		{"schedule floor", 2, 0, 4, 4}, {"KEDA dominates schedule", 4, 8, 5, 7},
		{"schedule dominates KEDA", 3, 4, 6, 6}, {"lowered max never shrinks", 12, 0, 2, 12},
		{"ceiling", 9, 10000, 0, 10}, {"minimum", 0, 0, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Desired(tc.current, tc.requested, tc.floor, c); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
func TestSchedules(t *testing.T) {
	ws := []api.Schedule{{Name: "office", TimeZone: "America/Maceio", Days: []int{1}, Start: "09:00", End: "18:00", Replicas: 5}, {Name: "peak", TimeZone: "America/Maceio", Days: []int{1}, Start: "10:00", End: "12:00", Replicas: 8}, {Name: "night", TimeZone: "America/Maceio", Days: []int{1}, Start: "22:00", End: "02:00", Replicas: 6}}
	for _, tc := range []struct {
		at   string
		want int32
	}{{"2026-10-12T11:59:00Z", 0}, {"2026-10-12T12:00:00Z", 5}, {"2026-10-12T13:00:00Z", 8}, {"2026-10-12T15:00:00Z", 5}, {"2026-10-12T21:00:00Z", 0}, {"2026-10-13T04:59:00Z", 6}, {"2026-10-13T05:00:00Z", 0}, {"2026-10-14T04:00:00Z", 0}} {
		t.Run(tc.at, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.at)
			got, e := ScheduleFloor(now, ws)
			if e != nil || got != tc.want {
				t.Fatalf("got %d err %v want %d", got, e, tc.want)
			}
		})
	}
}
func TestDST(t *testing.T) {
	ws := []api.Schedule{{TimeZone: "America/New_York", Days: []int{0}, Start: "01:00", End: "02:00", Replicas: 4}}
	for _, at := range []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"} {
		now, _ := time.Parse(time.RFC3339, at)
		got, e := ScheduleFloor(now, ws)
		if e != nil || got != 4 {
			t.Fatal(got, e)
		}
	}
}
func FuzzNoScaleDown(f *testing.F) {
	f.Add(int32(4), int32(999), int32(3))
	f.Fuzz(func(t *testing.T, current int32, requested int32, floor int32) {
		if current < 0 || current > 10000 || requested < 0 || floor < 0 {
			return
		}
		c := api.Scaling{MinReplicas: 1, MaxReplicas: 20, MaxStep: 2}
		got := Desired(current, requested, floor, c)
		if got < current || int64(got) > int64(current)+2 {
			t.Fatalf("unsafe %d -> %d", current, got)
		}
	})
}
