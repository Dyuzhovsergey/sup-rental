package rental

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceMonitoringBuildsMoscowSnapshot(t *testing.T) {
	location := time.FixedZone("test-msk", 3*60*60)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, location)
	upcoming := monitoringSummaryFixture(t, 1, StatusConfirmed, now.Add(30*time.Minute), now.Add(90*time.Minute))
	delayed := monitoringSummaryFixture(t, 2, StatusConfirmed, now.Add(-30*time.Minute), now.Add(30*time.Minute))
	active := monitoringSummaryFixture(t, 3, StatusActive, now.Add(-30*time.Minute), now.Add(90*time.Minute))
	overdue := monitoringSummaryFixture(t, 4, StatusActive, now.Add(-2*time.Hour), now.Add(-30*time.Minute))

	var gotQuery MonitoringQuery
	service := NewService(&serviceRepositoryStub{monitoring: func(
		_ context.Context, query MonitoringQuery,
	) (MonitoringData, error) {
		gotQuery = query
		return MonitoringData{
			TodayTotal: 4, ConfirmedTotal: 2, ActiveTotal: 2, OverdueTotal: 1,
			Confirmed: []Summary{upcoming, delayed}, Active: []Summary{overdue, active},
		}, nil
	}})
	service.now = func() time.Time { return now }

	snapshot, err := service.Monitoring(context.Background())
	if err != nil {
		t.Fatalf("Monitoring() error = %v", err)
	}
	if gotQuery.Limit != MonitoringListLimit || !gotQuery.Now.Equal(now) {
		t.Fatalf("query = %+v", gotQuery)
	}
	if gotQuery.DayStart.Format(time.RFC3339) != "2026-08-20T00:00:00+03:00" ||
		gotQuery.DayEnd.Format(time.RFC3339) != "2026-08-21T00:00:00+03:00" {
		t.Fatalf("day = %s — %s", gotQuery.DayStart.Format(time.RFC3339), gotQuery.DayEnd.Format(time.RFC3339))
	}
	if snapshot.TodayTotal != 4 || snapshot.ConfirmedTotal != 2 || snapshot.ActiveTotal != 2 || snapshot.OverdueTotal != 1 {
		t.Fatalf("snapshot counts = %+v", snapshot)
	}
	if snapshot.Confirmed[0].Timing.State != MonitoringUpcoming || snapshot.Confirmed[0].Timing.Delta != 30*time.Minute {
		t.Errorf("upcoming timing = %+v", snapshot.Confirmed[0].Timing)
	}
	if snapshot.Confirmed[1].Timing.State != MonitoringIssueDelayed || snapshot.Confirmed[1].Timing.Percent != 50 {
		t.Errorf("delayed timing = %+v", snapshot.Confirmed[1].Timing)
	}
	if snapshot.Active[0].Timing.State != MonitoringOverdue || snapshot.Active[0].Timing.Percent != 100 || snapshot.Active[0].Timing.Delta != 30*time.Minute {
		t.Errorf("overdue timing = %+v", snapshot.Active[0].Timing)
	}
	if snapshot.Active[1].Timing.State != MonitoringActive || snapshot.Active[1].Timing.Percent != 25 || snapshot.Active[1].Timing.Delta != 90*time.Minute {
		t.Errorf("active timing = %+v", snapshot.Active[1].Timing)
	}
}

func TestServiceMonitoringMarksExactEndAsDue(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	summary := monitoringSummaryFixture(t, 1, StatusActive, now.Add(-time.Hour), now)
	service := NewService(&serviceRepositoryStub{monitoring: func(
		context.Context, MonitoringQuery,
	) (MonitoringData, error) {
		return MonitoringData{ActiveTotal: 1, Active: []Summary{summary}}, nil
	}})
	service.now = func() time.Time { return now }

	snapshot, err := service.Monitoring(context.Background())
	if err != nil {
		t.Fatalf("Monitoring() error = %v", err)
	}
	if got := snapshot.Active[0].Timing; got.State != MonitoringDue || got.Percent != 100 || got.Delta != 0 {
		t.Fatalf("timing = %+v", got)
	}
}

func TestServiceMonitoringWrapsRepositoryErrorAndRejectsInvalidRows(t *testing.T) {
	repositoryError := errors.New("database unavailable")
	service := NewService(&serviceRepositoryStub{monitoring: func(
		context.Context, MonitoringQuery,
	) (MonitoringData, error) {
		return MonitoringData{}, repositoryError
	}})
	if _, err := service.Monitoring(context.Background()); !errors.Is(err, repositoryError) {
		t.Fatalf("Monitoring() error = %v", err)
	}

	service = NewService(&serviceRepositoryStub{monitoring: func(
		context.Context, MonitoringQuery,
	) (MonitoringData, error) {
		return MonitoringData{Confirmed: []Summary{{ID: 1, Status: StatusActive}}}, nil
	}})
	if _, err := service.Monitoring(context.Background()); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("Monitoring() invalid row error = %v", err)
	}
}

func monitoringSummaryFixture(t *testing.T, id int64, status Status, start, end time.Time) Summary {
	t.Helper()
	interval, err := NewInterval(start, end)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return Summary{
		ID: id, ClientID: id + 100, ClientName: "Клиент",
		Interval: interval, Status: status, ItemCount: 2, PlannedTotalKopecks: 100_000,
	}
}
