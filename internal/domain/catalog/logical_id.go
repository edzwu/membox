package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// MinLogicalIDLen is the default visible length of a logical document id.
// Longer suffixes are used only when needed to disambiguate collisions.
const MinLogicalIDLen = 4

// CompactID strips UUID dashes and lowercases hex so prefix/suffix logic can
// ignore presentation. Non-UUID selectors pass through lowercased.
func CompactID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

// LogicalIDs maps each physical UUID to a short logical id.
//
// Logical ids are the shortest unique suffix of the compact physical id, at
// least MinLogicalIDLen hex characters. UUID v7 shares long time-based
// prefixes, so suffixes carry the distinguishing entropy users need when
// typing a short selector in natural (left-to-right) order against the
// visible logical id.
//
// Physical ids themselves are never rewritten; this is display/resolve only.
func LogicalIDs(physicalIDs []string) map[string]string {
	type item struct {
		physical string
		compact  string
		rev      string
	}
	items := make([]item, 0, len(physicalIDs))
	seenPhysical := make(map[string]struct{}, len(physicalIDs))
	for _, raw := range physicalIDs {
		physical := strings.TrimSpace(raw)
		if physical == "" {
			continue
		}
		key := strings.ToLower(physical)
		if _, dup := seenPhysical[key]; dup {
			continue
		}
		seenPhysical[key] = struct{}{}
		compact := CompactID(physical)
		if compact == "" {
			continue
		}
		items = append(items, item{
			physical: physical,
			compact:  compact,
			rev:      reverseASCII(compact),
		})
	}
	out := make(map[string]string, len(items))
	if len(items) == 0 {
		return out
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rev == items[j].rev {
			return items[i].physical < items[j].physical
		}
		return items[i].rev < items[j].rev
	})
	for i, it := range items {
		need := MinLogicalIDLen
		if i > 0 {
			if common := commonPrefixLen(it.rev, items[i-1].rev); common+1 > need {
				need = common + 1
			}
		}
		if i+1 < len(items) {
			if common := commonPrefixLen(it.rev, items[i+1].rev); common+1 > need {
				need = common + 1
			}
		}
		if need > len(it.compact) {
			need = len(it.compact)
		}
		if need < 1 {
			need = 1
		}
		out[it.physical] = it.compact[len(it.compact)-need:]
	}
	return out
}

// LookupLogical returns the logical id for one physical id using a map from
// LogicalIDs. Falls back to a fixed-length compact suffix when unknown.
func LookupLogical(physical string, logicalByPhysical map[string]string) string {
	physical = strings.TrimSpace(physical)
	if physical == "" {
		return ""
	}
	if logicalByPhysical != nil {
		if logical, ok := logicalByPhysical[physical]; ok {
			return logical
		}
		if logical, ok := logicalByPhysical[strings.ToLower(physical)]; ok {
			return logical
		}
	}
	return FallbackLogicalID(physical)
}

// FallbackLogicalID is the corpus-free abbreviation used before a full
// logical map is available (tests, one-off formatting).
func FallbackLogicalID(physical string) string {
	compact := CompactID(physical)
	if compact == "" {
		return strings.TrimSpace(physical)
	}
	if len(compact) > MinLogicalIDLen {
		return compact[len(compact)-MinLogicalIDLen:]
	}
	return compact
}

// MatchLogicalSelector resolves a user-typed selector against physical ids
// and their logical abbreviations.
//
// Match order:
//  1. exact physical id (dashed or compact)
//  2. exact logical id
//  3. unique logical-id prefix (left-to-right on the visible short id)
//  4. unique physical compact suffix (legacy short-id / longer tails)
//  5. unique physical compact prefix (only useful for long pasted fragments)
//
// Ambiguous selectors return an error asking for a longer id. Empty input is
// not found.
func MatchLogicalSelector(selector string, logicalByPhysical map[string]string) (string, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "" {
		return "", fmt.Errorf("document %q not found", selector)
	}
	compactSelector := CompactID(selector)
	if compactSelector == "" {
		return "", fmt.Errorf("document %q not found", selector)
	}

	// Build reverse indexes once per call. Callers with hot paths should
	// cache via Store; this helper stays pure for tests and small sets.
	type entry struct {
		physical string
		compact  string
		logical  string
	}
	entries := make([]entry, 0, len(logicalByPhysical))
	seen := make(map[string]struct{}, len(logicalByPhysical))
	for physical, logical := range logicalByPhysical {
		key := strings.ToLower(strings.TrimSpace(physical))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		// Prefer the original casing from the first non-lower key when present.
		phys := strings.TrimSpace(physical)
		if lower := strings.ToLower(phys); lower != phys {
			// keep phys
		} else if orig := findOriginalPhysical(logicalByPhysical, key); orig != "" {
			phys = orig
		}
		seen[key] = struct{}{}
		entries = append(entries, entry{
			physical: phys,
			compact:  CompactID(phys),
			logical:  strings.ToLower(logical),
		})
	}

	var exactPhysical []string
	var exactLogical []string
	var logicalPrefix []string
	var physicalSuffix []string
	var physicalPrefix []string

	for _, e := range entries {
		lowerPhys := strings.ToLower(e.physical)
		if lowerPhys == selector || e.compact == compactSelector {
			exactPhysical = append(exactPhysical, e.physical)
			continue
		}
		if e.logical == compactSelector || e.logical == selector {
			exactLogical = append(exactLogical, e.physical)
		}
		if strings.HasPrefix(e.logical, compactSelector) {
			logicalPrefix = append(logicalPrefix, e.physical)
		}
		if strings.HasSuffix(e.compact, compactSelector) {
			physicalSuffix = append(physicalSuffix, e.physical)
		}
		if strings.HasPrefix(e.compact, compactSelector) || strings.HasPrefix(lowerPhys, selector) {
			physicalPrefix = append(physicalPrefix, e.physical)
		}
	}

	if id, ok, err := pickUnique("document", selector, exactPhysical); ok || err != nil {
		return id, err
	}
	if id, ok, err := pickUnique("document", selector, exactLogical); ok || err != nil {
		return id, err
	}
	if id, ok, err := pickUnique("document", selector, logicalPrefix); ok || err != nil {
		return id, err
	}
	if id, ok, err := pickUnique("document", selector, physicalSuffix); ok || err != nil {
		return id, err
	}
	// Physical prefix last: UUID v7 time prefixes collide heavily, so this
	// only succeeds when the fragment is already unique in the corpus.
	if id, ok, err := pickUnique("document", selector, physicalPrefix); ok || err != nil {
		return id, err
	}
	return "", fmt.Errorf("document %q not found", selector)
}

func findOriginalPhysical(m map[string]string, lower string) string {
	for physical := range m {
		if strings.ToLower(physical) == lower && physical != lower {
			return physical
		}
	}
	return lower
}

func pickUnique(kind, selector string, matches []string) (string, bool, error) {
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", true, fmt.Errorf("%s selector %q is ambiguous; use a longer id", kind, selector)
	}
}

func reverseASCII(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
