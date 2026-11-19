// Package engine implements deterministic, scale-up-only decisions.
package engine

import (
	"fmt"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"time"
)

func Validate(s api.ElasticsearchQueueAutoscalerSpec) error {
	c := s.Scaling
	if c.MinReplicas < 1 || c.MaxReplicas < c.MinReplicas || c.MaxReplicas > 10000 || c.MaxStep < 1 || c.PollSeconds < 15 || c.StabilizationSeconds < 0 || c.CooldownSeconds < 60 || (c.ConvergenceTimeoutSeconds != 0 && c.ConvergenceTimeoutSeconds < 60) {
		return fmt.Errorf("invalid scaling configuration")
	}
	if !s.Safety.BlockScaleDown || s.Safety.MaxDiskUsedPercent < 1 || s.Safety.MaxDiskUsedPercent > 100 || s.Safety.MaxShardsPerNode < 1 || (s.Safety.RequiredHealth != "green" && s.Safety.RequiredHealth != "yellow") {
		return fmt.Errorf("invalid safety configuration")
	}
	if s.Mode != "" && s.Mode != "Auto" && s.Mode != "Audit" && s.Mode != "DryRun" {
		return fmt.Errorf("invalid mode")
	}
	if (s.Provider != "" && s.Provider != "KEDA") || s.RequestedReplicas < 0 || s.RequestedReplicas > 10000 {
		return fmt.Errorf("invalid KEDA demand")
	}
	for _, w := range s.Sources.Schedules {
		if _, e := time.LoadLocation(w.TimeZone); e != nil {
			return e
		}
		a, e := minute(w.Start)
		if e != nil {
			return e
		}
		b, e := minute(w.End)
		if e != nil {
			return e
		}
		if a == b || w.Replicas < 1 || w.Replicas > c.MaxReplicas || len(w.Days) == 0 {
			return fmt.Errorf("invalid schedule %s", w.Name)
		}
		for _, d := range w.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("invalid weekday")
			}
		}
	}
	return nil
}
func minute(v string) (int, error) {
	t, e := time.Parse("15:04", v)
	if e != nil {
		return 0, e
	}
	return t.Hour()*60 + t.Minute(), nil
}

// ScheduleFloor uses civil time; overnight windows belong to their start day.
// DST repeated times are both active; skipped local times never occur.
func ScheduleFloor(now time.Time, windows []api.Schedule) (int32, error) {
	var floor int32
	for _, w := range windows {
		loc, e := time.LoadLocation(w.TimeZone)
		if e != nil {
			return 0, e
		}
		t := now.In(loc)
		start, e := minute(w.Start)
		if e != nil {
			return 0, e
		}
		end, e := minute(w.End)
		if e != nil {
			return 0, e
		}
		m := t.Hour()*60 + t.Minute()
		day := int(t.Weekday())
		active := false
		if start < end {
			active = m >= start && m < end
		} else if start > end {
			active = m >= start || m < end
			if m < end {
				day = (day + 6) % 7
			}
		}
		if active {
			for _, d := range w.Days {
				if d == day && w.Replicas > floor {
					floor = w.Replicas
				}
			}
		}
	}
	return floor, nil
}

// Desired never returns less than current, even when maxReplicas is lowered.
func Desired(current, requested, floor int32, c api.Scaling) int32 {
	demand := int64(c.MinReplicas)
	if int64(requested) > demand {
		demand = int64(requested)
	}
	if int64(floor) > demand {
		demand = int64(floor)
	}
	if demand > int64(c.MaxReplicas) {
		demand = int64(c.MaxReplicas)
	}
	if demand > int64(current)+int64(c.MaxStep) {
		demand = int64(current) + int64(c.MaxStep)
	}
	if demand < int64(current) {
		demand = int64(current)
	}
	return int32(demand)
}
