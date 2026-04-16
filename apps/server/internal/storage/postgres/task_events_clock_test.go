package postgres

import (
	"testing"
	"time"
)

func TestMonotonicTimestampClockNextBumpsDuplicateMicroseconds(t *testing.T) {
	var clock monotonicTimestampClock
	base := time.Date(2026, 4, 15, 12, 0, 0, 123456789, time.UTC)

	first := clock.Next(base)
	second := clock.Next(base)

	wantFirst := time.Date(2026, 4, 15, 12, 0, 0, 123456000, time.UTC)
	if !first.Equal(wantFirst) {
		t.Fatalf("expected first timestamp %v, got %v", wantFirst, first)
	}
	if !second.After(first) {
		t.Fatalf("expected second timestamp %v to be after first %v", second, first)
	}
	if second.Sub(first) != time.Microsecond {
		t.Fatalf("expected second timestamp to advance by 1 microsecond, got %v", second.Sub(first))
	}
}

func TestMonotonicTimestampClockNextKeepsForwardTime(t *testing.T) {
	var clock monotonicTimestampClock
	base := time.Date(2026, 4, 15, 12, 0, 0, 123456789, time.UTC)
	later := base.Add(5 * time.Millisecond)

	_ = clock.Next(base)
	second := clock.Next(later)

	want := time.Date(2026, 4, 15, 12, 0, 0, 128456000, time.UTC)
	if !second.Equal(want) {
		t.Fatalf("expected later timestamp %v, got %v", want, second)
	}
}
