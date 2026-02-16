package feed

import (
	"testing"
	"time"
)

func TestComputeBackoffInterval(t *testing.T) {
	cases := []int{0, 1, 2, 3, 4, 8}
	for _, count := range cases {
		want := RefreshInterval
		for i := 0; i < count; i++ {
			want *= 2
			if want >= 12*time.Hour {
				want = 12 * time.Hour
				break
			}
		}
		if want > 12*time.Hour {
			want = 12 * time.Hour
		}
		if got := ComputeBackoffInterval(count); got != want {
			t.Fatalf("count %d: expected %v, got %v", count, want, got)
		}
	}
}

func TestApplyJitterRange(t *testing.T) {
	base := RefreshInterval
	min := time.Duration(float64(base) * (1 - 0.20))
	max := time.Duration(float64(base) * (1 + 0.20))
	for i := 0; i < 10; i++ {
		got := ApplyJitter(base)
		if got < min || got > max {
			t.Fatalf("jittered value %v out of range (%v-%v)", got, min, max)
		}
	}
}
