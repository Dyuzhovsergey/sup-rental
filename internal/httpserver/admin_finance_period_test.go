package httpserver

import (
	"net/url"
	"testing"
	"time"
)

func TestParseAdminFinancePeriodPresetsAndCustomRange(t *testing.T) {
	now := time.Date(2027, 1, 2, 9, 30, 0, 0, moscowTimeZone)
	tests := []struct {
		name       string
		query      url.Values
		wantKey    string
		wantStart  string
		wantEnd    string
		wantPhrase string
	}{
		{name: "default today", query: url.Values{}, wantKey: financePeriodToday, wantStart: "2027-01-02T00:00:00+03:00", wantEnd: "2027-01-03T00:00:00+03:00", wantPhrase: "сегодня"},
		{name: "yesterday", query: url.Values{"period": {financePeriodYesterday}}, wantKey: financePeriodYesterday, wantStart: "2027-01-01T00:00:00+03:00", wantEnd: "2027-01-02T00:00:00+03:00", wantPhrase: "за вчера"},
		{name: "seven days", query: url.Values{"period": {financePeriodSevenDays}}, wantKey: financePeriodSevenDays, wantStart: "2026-12-27T00:00:00+03:00", wantEnd: "2027-01-03T00:00:00+03:00", wantPhrase: "за 7 дней"},
		{name: "custom inclusive end", query: url.Values{"period": {financePeriodCustom}, "from": {"2026-12-30"}, "to": {"2027-01-02"}}, wantKey: financePeriodCustom, wantStart: "2026-12-30T00:00:00+03:00", wantEnd: "2027-01-03T00:00:00+03:00", wantPhrase: "за 30.12.2026–02.01.2027"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseAdminFinancePeriod(test.query, now)
			if !got.valid() || got.Key != test.wantKey || got.Phrase != test.wantPhrase ||
				got.Period.Start.Format(time.RFC3339) != test.wantStart || got.Period.End.Format(time.RFC3339) != test.wantEnd {
				t.Fatalf("selection = %+v", got)
			}
		})
	}
}

func TestParseAdminFinancePeriodRejectsInvalidCustomRange(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 30, 0, 0, moscowTimeZone)
	tests := []struct {
		name  string
		query url.Values
		want  string
	}{
		{name: "unknown", query: url.Values{"period": {"all"}}, want: "Выберите допустимый финансовый период."},
		{name: "missing from", query: url.Values{"period": {financePeriodCustom}, "to": {"2026-08-31"}}, want: "Укажите дату начала."},
		{name: "missing to", query: url.Values{"period": {financePeriodCustom}, "from": {"2026-08-01"}}, want: "Укажите дату окончания."},
		{name: "bad date", query: url.Values{"period": {financePeriodCustom}, "from": {"bad"}, "to": {"2026-08-31"}}, want: "Введите корректную дату."},
		{name: "reversed", query: url.Values{"period": {financePeriodCustom}, "from": {"2026-08-10"}, "to": {"2026-08-08"}}, want: "Дата начала не может быть позже даты окончания."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseAdminFinancePeriod(test.query, now)
			if got.valid() || (got.FromError != test.want && got.ToError != test.want && got.PeriodError != test.want) {
				t.Fatalf("selection = %+v, want error %q", got, test.want)
			}
		})
	}
}

func TestFinancePeriodURLsPreserveSelectionAndPageSize(t *testing.T) {
	selection := adminFinancePeriodSelection{Key: financePeriodCustom, From: "2026-08-01", To: "2026-08-31"}
	if got := financeNavigationURL("/admin/payments", selection); got != "/admin/payments?from=2026-08-01&period=custom&to=2026-08-31" {
		t.Fatalf("financeNavigationURL() = %q", got)
	}
	if got := paymentPageURL(2, 15, selection); got != "/admin/payments?from=2026-08-01&page=2&page_size=15&period=custom&to=2026-08-31" {
		t.Fatalf("paymentPageURL() = %q", got)
	}
}
