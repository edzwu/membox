package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	lookupMaxDefinitions  = 3
	lookupMaxTerms        = 5
	lookupMaxWordRunes    = 40
	lookupCacheTTL        = 24 * time.Hour
	lookupCacheMaxEntries = 512
	lookupDefaultLang     = "en"
)

// gdictDBPath is overridable in tests.
var gdictDBPath = func() string {
	if p := strings.TrimSpace(os.Getenv("MEMBOX_GDICT_DB")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Same path as github.com/Lodobo/gdict
	return filepath.Join(home, ".local", "share", "gdict", "dictionary.db")
}

type lookupCacheEntry struct {
	result    lookupResult
	expiresAt time.Time
}

var (
	lookupCacheMu sync.Mutex
	lookupCache   = map[string]lookupCacheEntry{}

	gdictMu sync.Mutex
	gdictDB *sql.DB
)

type lookupPhonetic struct {
	Text  string `json:"text,omitempty"`
	Audio string `json:"audio,omitempty"`
}

type lookupDefinition struct {
	Definition string   `json:"definition"`
	Example    string   `json:"example,omitempty"`
	Synonyms   []string `json:"synonyms,omitempty"`
	Antonyms []string `json:"antonyms,omitempty"`
}

type lookupMeaning struct {
	PartOfSpeech string             `json:"part_of_speech"`
	Definitions  []lookupDefinition `json:"definitions"`
}

type lookupResult struct {
	Word      string           `json:"word"`
	Phonetics []lookupPhonetic `json:"phonetics,omitempty"`
	Meanings  []lookupMeaning  `json:"meanings"`
	Markdown  string           `json:"markdown"`
	Source    string           `json:"source"`
}

// gdict JSON column shapes (Wiktextract / kaikki dumps via gdict).
type gdictSound struct {
	IPA  string   `json:"ipa"`
	Tags []string `json:"tags"`
	Text string   `json:"text"`
}

type gdictSense struct {
	Glosses  []string `json:"glosses"`
	Tags     []string `json:"tags"`
	Examples []struct {
		Text string `json:"text"`
	} `json:"examples"`
	Synonyms []struct {
		Word string `json:"word"`
	} `json:"synonyms"`
	Antonyms []struct {
		Word string `json:"word"`
	} `json:"antonyms"`
}

func (s *Server) handleLookup(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	word := normalizeLookupWord(request.URL.Query().Get("q"))
	if word == "" {
		http.Error(writer, "missing or invalid word", http.StatusBadRequest)
		return
	}
	lang := strings.TrimSpace(request.URL.Query().Get("lang"))
	if lang == "" {
		lang = lookupDefaultLang
	}
	result, status, err := fetchDictionary(request.Context(), word, lang)
	if err != nil {
		http.Error(writer, err.Error(), status)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, max-age=3600")
	_ = json.NewEncoder(writer).Encode(result)
}

func normalizeLookupWord(raw string) string {
	word := strings.TrimSpace(raw)
	if word == "" {
		return ""
	}
	// Strip surrounding punctuation that often comes along with a drag selection.
	word = strings.TrimFunc(word, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	if word == "" || utf8.RuneCountInString(word) > lookupMaxWordRunes {
		return ""
	}
	if strings.ContainsAny(word, " \t\n\r") {
		return ""
	}
	hasLetter := false
	for _, r := range word {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			continue
		}
		if r == '\'' || r == '-' || r == '\u2019' {
			continue
		}
		return ""
	}
	if !hasLetter {
		return ""
	}
	return strings.ToLower(word)
}

func fetchDictionary(ctx context.Context, word, lang string) (lookupResult, int, error) {
	cacheKey := lang + "\x00" + word
	if cached, ok := lookupCacheGet(cacheKey); ok {
		return cached, http.StatusOK, nil
	}
	result, status, err := fetchDictionaryLocal(ctx, word, lang)
	if err == nil {
		lookupCachePut(cacheKey, result)
	}
	return result, status, err
}

func openGdictDB() (*sql.DB, error) {
	gdictMu.Lock()
	defer gdictMu.Unlock()
	if gdictDB != nil {
		return gdictDB, nil
	}
	path := gdictDBPath()
	if path == "" {
		return nil, fmt.Errorf("gdict database path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("gdict database not found at %s (install English via gdict; see README)", path)
	}
	// read-only, immutable shared cache — safe for concurrent lookups
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open gdict database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping gdict database: %w", err)
	}
	gdictDB = db
	return gdictDB, nil
}

func fetchDictionaryLocal(ctx context.Context, word, lang string) (lookupResult, int, error) {
	db, err := openGdictDB()
	if err != nil {
		return lookupResult{}, http.StatusServiceUnavailable, err
	}
	// Language tables are ISO codes installed by gdict (en, zh, …).
	if !isSafeGdictLang(lang) {
		return lookupResult{}, http.StatusBadRequest, fmt.Errorf("invalid language %q", lang)
	}
	// Verify table exists.
	var tableCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, lang,
	).Scan(&tableCount); err != nil {
		return lookupResult{}, http.StatusInternalServerError, fmt.Errorf("gdict schema check: %w", err)
	}
	if tableCount == 0 {
		return lookupResult{}, http.StatusServiceUnavailable, fmt.Errorf("language %q is not installed in gdict", lang)
	}

	// Table name is validated; word is bound.
	query := fmt.Sprintf(`SELECT word, pos, sounds, senses FROM [%s] WHERE word = ? COLLATE NOCASE`, lang)
	rows, err := db.QueryContext(ctx, query, word)
	if err != nil {
		return lookupResult{}, http.StatusInternalServerError, fmt.Errorf("gdict query: %w", err)
	}
	defer rows.Close()

	result := lookupResult{
		Word:   word,
		Source: "gdict (wiktionary offline)",
	}
	meaningsByPOS := map[string]*lookupMeaning{}
	var posOrder []string
	seenIPA := map[string]bool{}
	rowCount := 0
	for rows.Next() {
		rowCount++
		var (
			w, pos     string
			soundsJSON sql.NullString
			sensesJSON sql.NullString
		)
		if err := rows.Scan(&w, &pos, &soundsJSON, &sensesJSON); err != nil {
			return lookupResult{}, http.StatusInternalServerError, fmt.Errorf("gdict scan: %w", err)
		}
		if strings.TrimSpace(w) != "" {
			result.Word = w
		}
		pos = strings.TrimSpace(pos)
		if soundsJSON.Valid {
			for _, p := range parseGdictSounds(soundsJSON.String) {
				if p.Text == "" || seenIPA[p.Text] {
					continue
				}
				seenIPA[p.Text] = true
				result.Phonetics = append(result.Phonetics, p)
			}
		}
		if !sensesJSON.Valid || pos == "" {
			continue
		}
		meaning := meaningsByPOS[pos]
		if meaning == nil {
			meaning = &lookupMeaning{PartOfSpeech: pos}
			meaningsByPOS[pos] = meaning
			posOrder = append(posOrder, pos)
		}
		for _, def := range parseGdictSenses(sensesJSON.String) {
			if len(meaning.Definitions) >= lookupMaxDefinitions {
				break
			}
			if def.Definition == "" {
				continue
			}
			meaning.Definitions = append(meaning.Definitions, def)
		}
	}
	if err := rows.Err(); err != nil {
		return lookupResult{}, http.StatusInternalServerError, err
	}
	if rowCount == 0 {
		return lookupResult{}, http.StatusNotFound, fmt.Errorf("word not found")
	}
	for _, pos := range posOrder {
		m := meaningsByPOS[pos]
		if m == nil || len(m.Definitions) == 0 {
			continue
		}
		result.Meanings = append(result.Meanings, *m)
	}
	if len(result.Meanings) == 0 {
		return lookupResult{}, http.StatusNotFound, fmt.Errorf("word not found")
	}
	result.Markdown = formatLookupMarkdown(result)
	return result, http.StatusOK, nil
}

func isSafeGdictLang(lang string) bool {
	if lang == "" || len(lang) > 8 {
		return false
	}
	for _, r := range lang {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func parseGdictSounds(raw string) []lookupPhonetic {
	var sounds []gdictSound
	if json.Unmarshal([]byte(raw), &sounds) != nil {
		return nil
	}
	out := make([]lookupPhonetic, 0, len(sounds))
	for _, s := range sounds {
		ipa := strings.TrimSpace(s.IPA)
		if ipa == "" {
			// Some rows put IPA in text; skip audio-only stubs like "Audio (US)".
			candidate := strings.TrimSpace(s.Text)
			if strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "[") {
				ipa = candidate
			}
		}
		if ipa == "" || !(strings.HasPrefix(ipa, "/") || strings.HasPrefix(ipa, "[")) {
			continue
		}
		// Prefer bare IPA; keep tags only as annotation when useful.
		if len(s.Tags) > 0 && s.Tags[0] != "" {
			ipa = ipa + " (" + s.Tags[0] + ")"
		}
		out = append(out, lookupPhonetic{Text: ipa})
	}
	return out
}

func parseGdictSenses(raw string) []lookupDefinition {
	var senses []gdictSense
	if json.Unmarshal([]byte(raw), &senses) != nil {
		return nil
	}
	out := make([]lookupDefinition, 0, lookupMaxDefinitions)
	for _, sense := range senses {
		if len(out) >= lookupMaxDefinitions {
			break
		}
		glosses := sense.Glosses
		// gdict CLI skips glosses[0] when len>1 (often a category/header).
		if len(glosses) > 1 {
			glosses = glosses[1:]
		}
		def := strings.TrimSpace(strings.Join(glosses, "; "))
		if def == "" {
			continue
		}
		example := ""
		if len(sense.Examples) > 0 {
			example = strings.TrimSpace(sense.Examples[0].Text)
		}
		syns := make([]string, 0, lookupMaxTerms)
		for _, s := range sense.Synonyms {
			w := strings.TrimSpace(s.Word)
			if w == "" {
				continue
			}
			syns = append(syns, w)
			if len(syns) >= lookupMaxTerms {
				break
			}
		}
		ants := make([]string, 0, lookupMaxTerms)
		for _, a := range sense.Antonyms {
			w := strings.TrimSpace(a.Word)
			if w == "" {
				continue
			}
			ants = append(ants, w)
			if len(ants) >= lookupMaxTerms {
				break
			}
		}
		out = append(out, lookupDefinition{
			Definition: def,
			Example:    example,
			Synonyms:   syns,
			Antonyms: ants,
		})
	}
	return out
}

func lookupCacheGet(key string) (lookupResult, bool) {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	entry, ok := lookupCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(lookupCache, key)
		}
		return lookupResult{}, false
	}
	return entry.result, true
}

func lookupCachePut(key string, result lookupResult) {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	if len(lookupCache) >= lookupCacheMaxEntries {
		lookupCache = map[string]lookupCacheEntry{}
	}
	lookupCache[key] = lookupCacheEntry{result: result, expiresAt: time.Now().Add(lookupCacheTTL)}
}

func lookupCacheReset() {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	lookupCache = map[string]lookupCacheEntry{}
}

func closeGdictDB() {
	gdictMu.Lock()
	defer gdictMu.Unlock()
	if gdictDB != nil {
		_ = gdictDB.Close()
		gdictDB = nil
	}
}

// formatLookupMarkdown produces a plain Markdown note body ready for Save note.
func formatLookupMarkdown(result lookupResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", result.Word)
	var phonetics []string
	seen := map[string]bool{}
	for _, p := range result.Phonetics {
		if p.Text == "" || seen[p.Text] {
			continue
		}
		seen[p.Text] = true
		phonetics = append(phonetics, p.Text)
	}
	if len(phonetics) > 0 {
		fmt.Fprintf(&b, "*%s*\n\n", strings.Join(phonetics, " · "))
	}
	for _, m := range result.Meanings {
		pos := m.PartOfSpeech
		if pos == "" {
			pos = "meaning"
		}
		fmt.Fprintf(&b, "## %s\n\n", pos)
		for i, d := range m.Definitions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, d.Definition)
			if d.Example != "" {
				fmt.Fprintf(&b, "   > %s\n", d.Example)
			}
			if len(d.Synonyms) > 0 {
				fmt.Fprintf(&b, "   - syn: %s\n", strings.Join(d.Synonyms, ", "))
			}
			if len(d.Antonyms) > 0 {
				fmt.Fprintf(&b, "   - ant: %s\n", strings.Join(d.Antonyms, ", "))
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "source: %s\n", result.Source)
	return b.String()
}
