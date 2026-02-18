//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"testing"
	"time"
)

const (
	backoffCountZero  = 0
	backoffCountOne   = 1
	backoffCountTwo   = 2
	backoffCountThree = 3
	backoffCountFour  = 4
	backoffCountEight = 8
	testBackoffFactor = 2
	backoffCap        = 12 * time.Hour
	maxJitterFraction = 0.20
	jitterSampleCount = 10
)

func TestComputeBackoffInterval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		count int
		want  time.Duration
	}{
		{count: backoffCountZero, want: RefreshInterval},
		{count: backoffCountOne, want: RefreshInterval * testBackoffFactor},
		{
			count: backoffCountTwo,
			want:  RefreshInterval * testBackoffFactor * testBackoffFactor,
		},
		{
			count: backoffCountThree,
			want: RefreshInterval *
				testBackoffFactor *
				testBackoffFactor *
				testBackoffFactor,
		},
		{
			count: backoffCountFour,
			want: RefreshInterval *
				testBackoffFactor *
				testBackoffFactor *
				testBackoffFactor *
				testBackoffFactor,
		},
		{count: backoffCountEight, want: backoffCap},
	}

	for _, tc := range cases {
		if got := ComputeBackoffInterval(tc.count); got != tc.want {
			t.Fatalf(
				"count %d: expected %v, got %v",
				tc.count,
				tc.want,
				got,
			)
		}
	}
}

func TestApplyJitterRange(t *testing.T) {
	t.Parallel()

	base := RefreshInterval
	lowerBound := time.Duration(float64(base) * (1 - maxJitterFraction))

	upperBound := time.Duration(float64(base) * (1 + maxJitterFraction))
	for range jitterSampleCount {
		got := ApplyJitter(base)
		if got < lowerBound || got > upperBound {
			t.Fatalf(
				"jittered value %v out of range (%v-%v)",
				got,
				lowerBound,
				upperBound,
			)
		}
	}
}
