package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"membox"
)

type dateFilterField uint8

const (
	dateFilterModified dateFilterField = iota
	dateFilterCreated
)

type dateFilter struct {
	Label string
	Field dateFilterField
	Start *time.Time
	End   *time.Time // exclusive
}

func (filter dateFilter) matches(document membox.DocumentView) bool {
	value := document.UpdatedAt
	if filter.Field == dateFilterCreated {
		value = document.CreatedAt
	}
	if value.IsZero() {
		return false
	}
	if filter.Start != nil && value.Before(*filter.Start) {
		return false
	}
	return filter.End == nil || value.Before(*filter.End)
}

func matchesDateFilters(document membox.DocumentView, filters []dateFilter) bool {
	for _, filter := range filters {
		if !filter.matches(document) {
			return false
		}
	}
	return true
}

type datePeriod struct {
	start     time.Time
	end       time.Time
	canonical string
}

func parseDateFilter(token string, now time.Time) (dateFilter, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "+") {
		return dateFilter{}, errors.New("date filter must start with +")
	}
	expression := strings.TrimPrefix(token, "+")
	field := dateFilterModified
	prefix := "+"
	switch {
	case strings.HasPrefix(expression, "m:"):
		expression = strings.TrimPrefix(expression, "m:")
		prefix = "+m:"
	case strings.HasPrefix(expression, "c:"):
		expression = strings.TrimPrefix(expression, "c:")
		field = dateFilterCreated
		prefix = "+c:"
	}
	if expression == "" {
		return dateFilter{}, errors.New("date filter is empty")
	}
	if strings.Contains(expression, ":") {
		return dateFilter{}, fmt.Errorf("unknown date field in %q; use m: or c:", token)
	}

	parts := strings.Split(expression, "..")
	if len(parts) > 2 {
		return dateFilter{}, fmt.Errorf("invalid date range %q", expression)
	}
	if len(parts) == 1 {
		period, err := parseDatePeriod(parts[0], now)
		if err != nil {
			return dateFilter{}, err
		}
		start, end := period.start, period.end
		return dateFilter{Label: prefix + period.canonical, Field: field, Start: &start, End: &end}, nil
	}

	var filter dateFilter
	filter.Field = field
	leftLabel, rightLabel := "", ""
	if parts[0] != "" {
		period, err := parseDatePeriod(parts[0], now)
		if err != nil {
			return dateFilter{}, err
		}
		start := period.start
		filter.Start = &start
		leftLabel = period.canonical
	}
	if parts[1] != "" {
		period, err := parseDatePeriod(parts[1], now)
		if err != nil {
			return dateFilter{}, err
		}
		end := period.end
		filter.End = &end
		rightLabel = period.canonical
	}
	if filter.Start == nil && filter.End == nil {
		return dateFilter{}, errors.New("date range needs a start or end")
	}
	if filter.Start != nil && filter.End != nil && !filter.Start.Before(*filter.End) {
		return dateFilter{}, fmt.Errorf("date range starts after it ends: %q", expression)
	}
	filter.Label = prefix + leftLabel + ".." + rightLabel
	return filter, nil
}

func parseDatePeriod(value string, now time.Time) (datePeriod, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	location := now.Location()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	switch value {
	case "today":
		return datePeriod{start: startOfToday, end: startOfToday.AddDate(0, 0, 1), canonical: "today"}, nil
	case "this-year":
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, location)
		return datePeriod{start: start, end: start.AddDate(1, 0, 0), canonical: "this-year"}, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err == nil && days > 0 {
			start := startOfToday.AddDate(0, 0, -days+1)
			return datePeriod{start: start, end: startOfToday.AddDate(0, 0, 1), canonical: fmt.Sprintf("%dd", days)}, nil
		}
	}

	// Accept xx wildcards for discoverability, but normalize them away in the
	// rendered tag because date precision already expresses the same period.
	value = strings.TrimSuffix(value, "-xx")
	value = strings.TrimSuffix(value, "-xx")
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 3 || len(parts[0]) != 4 {
		return datePeriod{}, fmt.Errorf("invalid date %q", value)
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year < 1 {
		return datePeriod{}, fmt.Errorf("invalid year in %q", value)
	}
	month, day := 1, 1
	if len(parts) >= 2 {
		if len(parts[1]) != 2 {
			return datePeriod{}, fmt.Errorf("invalid month in %q", value)
		}
		month, err = strconv.Atoi(parts[1])
		if err != nil || month < 1 || month > 12 {
			return datePeriod{}, fmt.Errorf("invalid month in %q", value)
		}
	}
	if len(parts) == 3 {
		if len(parts[2]) != 2 {
			return datePeriod{}, fmt.Errorf("invalid day in %q", value)
		}
		day, err = strconv.Atoi(parts[2])
		if err != nil || day < 1 || day > 31 {
			return datePeriod{}, fmt.Errorf("invalid day in %q", value)
		}
	}
	start := time.Date(year, time.Month(month), day, 0, 0, 0, 0, location)
	if start.Year() != year || int(start.Month()) != month || start.Day() != day {
		return datePeriod{}, fmt.Errorf("invalid calendar date %q", value)
	}
	period := datePeriod{start: start}
	switch len(parts) {
	case 1:
		period.end = start.AddDate(1, 0, 0)
		period.canonical = fmt.Sprintf("%04d", year)
	case 2:
		period.end = start.AddDate(0, 1, 0)
		period.canonical = fmt.Sprintf("%04d-%02d", year, month)
	case 3:
		period.end = start.AddDate(0, 0, 1)
		period.canonical = fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	}
	return period, nil
}
