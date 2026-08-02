package tui

import (
	"testing"
	"time"

	"membox"
)

func TestParseDateFilterMonthAndWildcard(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	for _, input := range []string{"+2026-07", "+2026-07-xx"} {
		filter, err := parseDateFilter(input, now)
		if err != nil {
			t.Fatalf("parse %s: %v", input, err)
		}
		if filter.Label != "+2026-07" || filter.Field != dateFilterModified {
			t.Fatalf("unexpected filter for %s: %+v", input, filter)
		}
		if got := filter.Start.Format("2006-01-02"); got != "2026-07-01" {
			t.Fatalf("start=%s", got)
		}
		if got := filter.End.Format("2006-01-02"); got != "2026-08-01" {
			t.Fatalf("end=%s", got)
		}
	}
}

func TestParseDateFilterInclusiveRangeAndOpenEnds(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	filter, err := parseDateFilter("+c:2026-07..2026-09", now)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Label != "+c:2026-07..2026-09" || filter.Field != dateFilterCreated {
		t.Fatalf("unexpected filter: %+v", filter)
	}
	if got := filter.Start.Format("2006-01-02"); got != "2026-07-01" {
		t.Fatalf("start=%s", got)
	}
	if got := filter.End.Format("2006-01-02"); got != "2026-10-01" {
		t.Fatalf("exclusive end=%s", got)
	}

	before, err := parseDateFilter("+..2026-07", now)
	if err != nil || before.Start != nil || before.End.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("unexpected open-start filter: %+v err=%v", before, err)
	}
	after, err := parseDateFilter("+2026-07..", now)
	if err != nil || after.End != nil || after.Start.Format("2006-01-02") != "2026-07-01" {
		t.Fatalf("unexpected open-end filter: %+v err=%v", after, err)
	}
}

func TestParseDateFilterRelativePeriodsAndInvalidDates(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	filter, err := parseDateFilter("+m:7d", now)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Start.Format("2006-01-02") != "2026-07-27" || filter.End.Format("2006-01-02") != "2026-08-03" {
		t.Fatalf("unexpected seven-day period: %+v", filter)
	}
	for _, input := range []string{"+2026-02-30", "+2026-13", "+2026-09..2026-07", "+..", "+wat"} {
		if _, err := parseDateFilter(input, now); err == nil {
			t.Fatalf("expected %s to fail", input)
		}
	}
}

func TestDateFiltersMatchWithANDSemantics(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	modified, _ := parseDateFilter("+2026-07", now)
	created, _ := parseDateFilter("+c:2025", now)
	document := membox.DocumentView{
		CreatedAt: time.Date(2025, 4, 1, 0, 0, 0, 0, time.Local),
		UpdatedAt: time.Date(2026, 7, 15, 0, 0, 0, 0, time.Local),
	}
	if !matchesDateFilters(document, []dateFilter{modified, created}) {
		t.Fatal("document should match both filters")
	}
	document.CreatedAt = time.Date(2024, 4, 1, 0, 0, 0, 0, time.Local)
	if matchesDateFilters(document, []dateFilter{modified, created}) {
		t.Fatal("date filters were not combined with AND")
	}
}
