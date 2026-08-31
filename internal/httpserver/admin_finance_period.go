package httpserver

import (
	"net/url"
	"strconv"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
)

const (
	financePeriodToday     = "today"
	financePeriodYesterday = "yesterday"
	financePeriodSevenDays = "7d"
	financePeriodCustom    = "custom"
)

type adminFinancePeriodSelection struct {
	Period      dashboard.FinancialPeriod
	Key         string
	From        string
	To          string
	Phrase      string
	FromError   string
	ToError     string
	PeriodError string
}

type adminFinancePeriodView struct {
	Action          string
	Key             string
	From            string
	To              string
	FromError       string
	ToError         string
	PeriodError     string
	TodayURL        string
	YesterdayURL    string
	SevenDaysURL    string
	TodayActive     bool
	YesterdayActive bool
	SevenDaysActive bool
	CustomActive    bool
	PageSize        int
}

func parseAdminFinancePeriod(values url.Values, now time.Time) adminFinancePeriodSelection {
	key := values.Get("period")
	if key == "" {
		key = financePeriodToday
	}

	localNow := now.In(moscowTimeZone)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, moscowTimeZone)
	todayEnd := todayStart.AddDate(0, 0, 1)
	selection := adminFinancePeriodSelection{Key: key}

	switch key {
	case financePeriodToday:
		selection.Period, _ = dashboard.NewFinancialPeriod(todayStart, todayEnd)
		selection.Phrase = "сегодня"
	case financePeriodYesterday:
		selection.Period, _ = dashboard.NewFinancialPeriod(todayStart.AddDate(0, 0, -1), todayStart)
		selection.Phrase = "за вчера"
	case financePeriodSevenDays:
		selection.Period, _ = dashboard.NewFinancialPeriod(todayStart.AddDate(0, 0, -6), todayEnd)
		selection.Phrase = "за 7 дней"
	case financePeriodCustom:
		selection.From = values.Get("from")
		selection.To = values.Get("to")
		if selection.From == "" {
			selection.FromError = "Укажите дату начала."
		}
		if selection.To == "" {
			selection.ToError = "Укажите дату окончания."
		}
		from, fromError := parseMoscowDate(selection.From, false)
		to, toError := parseMoscowDate(selection.To, true)
		if selection.FromError == "" {
			selection.FromError = fromError
		}
		if selection.ToError == "" {
			selection.ToError = toError
		}
		if selection.FromError != "" || selection.ToError != "" {
			return selection
		}
		if !to.After(*from) {
			selection.PeriodError = "Дата начала не может быть позже даты окончания."
			return selection
		}
		selection.Period, _ = dashboard.NewFinancialPeriod(*from, *to)
		selection.Phrase = "за " + from.Format("02.01.2006") + "–" + to.AddDate(0, 0, -1).Format("02.01.2006")
	default:
		selection.PeriodError = "Выберите допустимый финансовый период."
	}
	return selection
}

func (s adminFinancePeriodSelection) valid() bool {
	return s.Period.Valid() && s.FromError == "" && s.ToError == "" && s.PeriodError == ""
}

func (s adminFinancePeriodSelection) queryValues() url.Values {
	values := url.Values{"period": {s.Key}}
	if s.Key == financePeriodCustom {
		values.Set("from", s.From)
		values.Set("to", s.To)
	}
	return values
}

func (s adminFinancePeriodSelection) view(action string, pageSize int) adminFinancePeriodView {
	return adminFinancePeriodView{
		Action: action, Key: s.Key, From: s.From, To: s.To,
		FromError: s.FromError, ToError: s.ToError, PeriodError: s.PeriodError,
		TodayURL:        financePeriodURL(action, financePeriodToday, "", "", pageSize),
		YesterdayURL:    financePeriodURL(action, financePeriodYesterday, "", "", pageSize),
		SevenDaysURL:    financePeriodURL(action, financePeriodSevenDays, "", "", pageSize),
		TodayActive:     s.Key == financePeriodToday,
		YesterdayActive: s.Key == financePeriodYesterday,
		SevenDaysActive: s.Key == financePeriodSevenDays,
		CustomActive:    s.Key == financePeriodCustom,
		PageSize:        pageSize,
	}
}

func financePeriodURL(path, key, from, to string, pageSize int) string {
	values := url.Values{"period": {key}}
	if key == financePeriodCustom {
		values.Set("from", from)
		values.Set("to", to)
	}
	if pageSize > 0 {
		values.Set("page_size", strconv.Itoa(pageSize))
	}
	return path + "?" + values.Encode()
}

func financeNavigationURL(path string, selection adminFinancePeriodSelection) string {
	return path + "?" + selection.queryValues().Encode()
}
