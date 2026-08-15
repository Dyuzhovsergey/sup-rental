package rental

import (
	"errors"
	"testing"
	"time"
)

func TestNewInterval(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("Europe/Moscow", 3*60*60)
	validStart := time.Date(2026, time.August, 14, 10, 8, 0, 0, location)

	tests := []struct {
		name      string
		start     time.Time
		end       time.Time
		wantSlots int
		wantErr   error
	}{
		{
			name:      "one slot",
			start:     validStart,
			end:       validStart.Add(30 * time.Minute),
			wantSlots: 1,
		},
		{
			name:      "several slots",
			start:     validStart,
			end:       validStart.Add(90 * time.Minute),
			wantSlots: 3,
		},
		{
			name:      "maximum duration",
			start:     validStart,
			end:       validStart.Add(MaxDuration),
			wantSlots: int(MaxDuration / SlotDuration),
		},
		{
			name:      "one day from arbitrary minute",
			start:     validStart,
			end:       validStart.Add(24 * time.Hour),
			wantSlots: 48,
		},
		{
			name:    "start is required",
			end:     validStart.Add(30 * time.Minute),
			wantErr: ErrStartTimeRequired,
		},
		{
			name:    "end is required",
			start:   validStart,
			wantErr: ErrEndTimeRequired,
		},
		{
			name:    "equal boundaries",
			start:   validStart,
			end:     validStart,
			wantErr: ErrEndNotAfterStart,
		},
		{
			name:    "end before start",
			start:   validStart,
			end:     validStart.Add(-30 * time.Minute),
			wantErr: ErrEndNotAfterStart,
		},
		{
			name:    "shorter than one slot",
			start:   validStart,
			end:     validStart.Add(15 * time.Minute),
			wantErr: ErrIntervalTooShort,
		},
		{
			name:    "start contains seconds",
			start:   validStart.Add(time.Second),
			end:     validStart.Add(time.Second + time.Hour),
			wantErr: ErrStartNotMinuteAligned,
		},
		{
			name:    "duration is not aligned",
			start:   validStart,
			end:     validStart.Add(45 * time.Minute),
			wantErr: ErrDurationNotAligned,
		},
		{
			name:    "duration is too long",
			start:   validStart,
			end:     validStart.Add(MaxDuration + SlotDuration),
			wantErr: ErrIntervalTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			interval, err := NewInterval(tt.start, tt.end)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewInterval() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if !interval.Start().Equal(tt.start) {
				t.Errorf("Start() = %v, want %v", interval.Start(), tt.start)
			}
			if !interval.End().Equal(tt.end) {
				t.Errorf("End() = %v, want %v", interval.End(), tt.end)
			}
			if got := interval.SlotCount(); got != tt.wantSlots {
				t.Errorf("SlotCount() = %d, want %d", got, tt.wantSlots)
			}
		})
	}
}

func TestIntervalOverlaps(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))

	tests := []struct {
		name  string
		other Interval
		want  bool
	}{
		{
			name:  "overlapping interval",
			other: mustInterval(t, start.Add(30*time.Minute), start.Add(90*time.Minute)),
			want:  true,
		},
		{
			name:  "same interval",
			other: mustInterval(t, start, start.Add(time.Hour)),
			want:  true,
		},
		{
			name:  "adjacent after",
			other: mustInterval(t, start.Add(time.Hour), start.Add(2*time.Hour)),
			want:  false,
		},
		{
			name:  "adjacent before",
			other: mustInterval(t, start.Add(-time.Hour), start),
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := interval.Overlaps(tt.other); got != tt.want {
				t.Errorf("Overlaps() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestZeroIntervalSlotCount(t *testing.T) {
	t.Parallel()

	if got := (Interval{}).SlotCount(); got != 0 {
		t.Fatalf("SlotCount() = %d, want 0", got)
	}
}

func mustInterval(t *testing.T, start, end time.Time) Interval {
	t.Helper()

	interval, err := NewInterval(start, end)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return interval
}
