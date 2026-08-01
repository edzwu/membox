package catalog

// Relocation binds one previously known document to one new filesystem observation.
type Relocation struct {
	Document    *Document
	Observation Observation
	Reason      string
}

type RenameCandidate struct {
	Document    *Document
	Observation Observation
	Reason      string
}

type ReconcileResult struct {
	Relocations []Relocation
	Candidates  []RenameCandidate
	Missing     []*Document
	New         []Observation
}

// ReconcileRenames conservatively pairs missing documents with new observations.
// It first uses unique non-empty filesystem file keys, then unique exact hashes.
func ReconcileRenames(missing []*Document, incoming []Observation) ReconcileResult {
	result := ReconcileResult{}
	remainingDocs := append([]*Document(nil), missing...)
	remainingObs := append([]Observation(nil), incoming...)

	var matches []Relocation
	matches, remainingDocs, remainingObs = uniqueMatches(remainingDocs, remainingObs,
		func(d *Document) string { return string(d.FileKey) },
		func(o Observation) string { return string(o.FileKey) }, "file-key")
	result.Relocations = append(result.Relocations, matches...)

	matches, remainingDocs, remainingObs = uniqueMatches(remainingDocs, remainingObs,
		func(d *Document) string { return d.Index.SHA256 },
		func(o Observation) string { return o.SHA256 }, "exact-hash")
	result.Relocations = append(result.Relocations, matches...)

	// Any remaining equal-hash pair is deliberately ambiguous. Report a bounded
	// cross-product as candidates; callers must not turn these into identities.
	for _, d := range remainingDocs {
		for _, o := range remainingObs {
			if d.Index.SHA256 != "" && d.Index.SHA256 == o.SHA256 {
				result.Candidates = append(result.Candidates, RenameCandidate{Document: d, Observation: o, Reason: "ambiguous-exact-hash"})
			}
		}
	}
	result.Missing = remainingDocs
	result.New = remainingObs
	return result
}

func uniqueMatches(
	docs []*Document,
	observations []Observation,
	docKey func(*Document) string,
	observationKey func(Observation) string,
	reason string,
) ([]Relocation, []*Document, []Observation) {
	docCounts := make(map[string]int)
	obsCounts := make(map[string]int)
	for _, d := range docs {
		if key := docKey(d); key != "" {
			docCounts[key]++
		}
	}
	for _, o := range observations {
		if key := observationKey(o); key != "" {
			obsCounts[key]++
		}
	}

	usedDocs := make(map[int]bool)
	usedObs := make(map[int]bool)
	var matches []Relocation
	for di, d := range docs {
		key := docKey(d)
		if key == "" || docCounts[key] != 1 || obsCounts[key] != 1 {
			continue
		}
		for oi, o := range observations {
			if !usedObs[oi] && observationKey(o) == key {
				matches = append(matches, Relocation{Document: d, Observation: o, Reason: reason})
				usedDocs[di], usedObs[oi] = true, true
				break
			}
		}
	}

	leftDocs := make([]*Document, 0, len(docs)-len(matches))
	for i, d := range docs {
		if !usedDocs[i] {
			leftDocs = append(leftDocs, d)
		}
	}
	leftObs := make([]Observation, 0, len(observations)-len(matches))
	for i, o := range observations {
		if !usedObs[i] {
			leftObs = append(leftObs, o)
		}
	}
	return matches, leftDocs, leftObs
}
