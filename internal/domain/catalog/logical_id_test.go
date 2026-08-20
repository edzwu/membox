package catalog

import (
	"strings"
	"testing"
)

func TestLogicalIDs_UniqueSuffixGrowsOnCollision(t *testing.T) {
	// Share a 4-char compact suffix so the default length must grow.
	ids := []string{
		"019fbde8-1765-7f72-a52f-0e0606aa0000",
		"019fbde8-1765-7f72-a52f-0e0606bb0000",
		"01a014b8-aaaa-7bbb-8ccc-ddddeeee0001",
		"01a014b8-aaaa-7bbb-8ccc-ddddeeee0002",
		"01a014b8-ffff-7bbb-8ccc-abcdef12aaaa",
	}
	got := LogicalIDs(ids)
	if len(got) != len(ids) {
		t.Fatalf("expected %d logical ids, got %d", len(ids), len(got))
	}
	seen := map[string]string{}
	for _, id := range ids {
		logical := got[id]
		if len(logical) < MinLogicalIDLen {
			t.Fatalf("%s logical %q shorter than min", id, logical)
		}
		if !strings.HasSuffix(CompactID(id), logical) {
			t.Fatalf("%s logical %q is not a compact suffix", id, logical)
		}
		if other, ok := seen[logical]; ok {
			t.Fatalf("logical collision %q for %s and %s", logical, other, id)
		}
		seen[logical] = id
	}
	// Near-identical tails must lengthen past the default 4.
	if got[ids[0]] == got[ids[1]] {
		t.Fatal("colliding tails were not disambiguated")
	}
	if got[ids[0]] != "a0000" || got[ids[1]] != "b0000" {
		t.Fatalf("expected lengthened logical ids a0000/b0000, got %q and %q", got[ids[0]], got[ids[1]])
	}
}

func TestMatchLogicalSelector_PrefersVisibleLogicalPrefix(t *testing.T) {
	ids := []string{
		"01a014b8-aaaa-7bbb-8ccc-1234567890ab",
		"01a014b8-aaaa-7bbb-8ccc-1234567890cd",
		"019fbde8-1765-7f72-a52f-0e0606d1ffff",
	}
	logical := LogicalIDs(ids)

	// Physical time-prefix is ambiguous under UUID v7.
	if _, err := MatchLogicalSelector("01a014b8", logical); err == nil {
		t.Fatal("expected ambiguous physical prefix")
	}

	// Exact logical id works left-to-right against the visible short form.
	target := ids[2]
	short := logical[target]
	got, err := MatchLogicalSelector(short, logical)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("got %s want %s", got, target)
	}

	// Unique logical prefix also works.
	if len(short) > MinLogicalIDLen {
		prefix := short[:len(short)-1]
		got, err = MatchLogicalSelector(prefix, logical)
		if err != nil {
			t.Fatal(err)
		}
		if got != target {
			t.Fatalf("prefix match got %s want %s", got, target)
		}
	}

	// Full physical id always wins.
	got, err = MatchLogicalSelector(ids[0], logical)
	if err != nil {
		t.Fatal(err)
	}
	if got != ids[0] {
		t.Fatalf("full id got %s", got)
	}
}

func TestFallbackLogicalID_UsesCompactSuffix(t *testing.T) {
	id := "019fbde8-1765-7f72-a52f-0e0606d10000"
	if got := FallbackLogicalID(id); got != "0000" {
		t.Fatalf("got %q", got)
	}
}
