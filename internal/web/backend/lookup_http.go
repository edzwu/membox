package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Free Dictionary API — same source as ~/repo/lookup (github.com/mkaz/lookup).
// The public host is free and frequently returns 502/503 under load; callers
// retry briefly and cache hits so Miru does not surface every blip.
const dictionaryAPIURL = "https://api.dictionaryapi.dev/api/v2/entries/en/"

const (
	lookupMaxDefinitions = 3
	lookupMaxTerms       = 5
	lookupMaxWordRunes   = 40
	lookupHTTPTimeout    = 8 * time.Second
	lookupMaxAttempts    = 3
	lookupCacheTTL       = 24 * time.Hour
	lookupCacheMaxEntries = 512
)

// dictionaryHTTPClient is replaced in tests.
var dictionaryHTTPClient = &http.Client{Timeout: lookupHTTPTimeout}

// dictionarySleep is replaced in tests so retries do not wall-clock wait.
var dictionarySleep = time.Sleep

type lookupCacheEntry struct {
	result    lookupResult
	expiresAt time.Time
}

var (
	lookupCacheMu sync.Mutex
	lookupCache   = map[string]lookupCacheEntry{}
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

// Raw Free Dictionary API shapes.
type freeDictEntry struct {
	Word      string `json:"word"`
	Phonetics []struct {
		Text  string `json:"text"`
		Audio string `json:"audio"`
	} `json:"phonetics"`
	Meanings []struct {
		PartOfSpeech string `json:"partOfSpeech"`
		Definitions  []struct {
			Definition string   `json:"definition"`
			Example    string   `json:"example"`
			Synonyms   []string `json:"synonyms"`
			Antonyms []string `json:"antonyms"`
		} `json:"definitions"`
	} `json:"meanings"`
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
	result, status, err := fetchDictionary(request.Context(), word)
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
	// Single-token Latin/word-like forms only. Free Dictionary is English;
	// multi-word phrases and pure CJK selections are out of scope for now.
	if strings.ContainsAny(word, " \t\n\r") {
		return ""
	}
	hasLetter := false
	for _, r := range word {
		// Free Dictionary (and ~/repo/lookup) cover English headwords.
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

func fetchDictionary(ctx context.Context, word string) (lookupResult, int, error) {
	if cached, ok := lookupCacheGet(word); ok {
		return cached, http.StatusOK, nil
	}
	var lastStatus int
	var lastErr error
	for attempt := 1; attempt <= lookupMaxAttempts; attempt++ {
		result, status, err := fetchDictionaryOnce(ctx, word)
		if err == nil {
			lookupCachePut(word, result)
			return result, http.StatusOK, nil
		}
		lastStatus, lastErr = status, err
		// 404 is definitive; do not burn retries on missing headwords.
		if status == http.StatusNotFound || status == http.StatusBadRequest {
			return lookupResult{}, status, err
		}
		if attempt == lookupMaxAttempts || ctx.Err() != nil {
			break
		}
		// 200ms, 400ms — enough to ride out Free Dictionary blips without
		// making the toolbar feel stuck.
		dictionarySleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	if lastStatus == 0 {
		lastStatus = http.StatusBadGateway
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dictionary service temporarily unavailable")
	}
	return lookupResult{}, lastStatus, lastErr
}

func fetchDictionaryOnce(ctx context.Context, word string) (lookupResult, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dictionaryAPIURL+url.PathEscape(word), nil)
	if err != nil {
		return lookupResult{}, http.StatusInternalServerError, fmt.Errorf("building dictionary request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Identify ourselves; empty UA is more likely to be throttled/blocked.
	req.Header.Set("User-Agent", "membox-lookup/1.0 (+https://github.com/membox)")
	resp, err := dictionaryHTTPClient.Do(req)
	if err != nil {
		return lookupResult{}, http.StatusBadGateway, fmt.Errorf("dictionary service unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return lookupResult{}, http.StatusBadGateway, fmt.Errorf("reading dictionary response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return lookupResult{}, http.StatusNotFound, fmt.Errorf("word not found")
	}
	if resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusBadGateway ||
		resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusGatewayTimeout {
		return lookupResult{}, http.StatusBadGateway, fmt.Errorf("dictionary service temporarily unavailable (upstream HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return lookupResult{}, http.StatusBadGateway, fmt.Errorf("dictionary service error (upstream HTTP %d)", resp.StatusCode)
	}
	var entries []freeDictEntry
	if err := json.Unmarshal(body, &entries); err != nil || len(entries) == 0 {
		return lookupResult{}, http.StatusBadGateway, fmt.Errorf("invalid dictionary response")
	}
	return shapeLookupResult(entries[0]), http.StatusOK, nil
}

func lookupCacheGet(word string) (lookupResult, bool) {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	entry, ok := lookupCache[word]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(lookupCache, word)
		}
		return lookupResult{}, false
	}
	return entry.result, true
}

func lookupCachePut(word string, result lookupResult) {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	// Bound memory with a crude full wipe; lookups are tiny and rare.
	if len(lookupCache) >= lookupCacheMaxEntries {
		lookupCache = map[string]lookupCacheEntry{}
	}
	lookupCache[word] = lookupCacheEntry{result: result, expiresAt: time.Now().Add(lookupCacheTTL)}
}

func lookupCacheReset() {
	lookupCacheMu.Lock()
	defer lookupCacheMu.Unlock()
	lookupCache = map[string]lookupCacheEntry{}
}

func shapeLookupResult(entry freeDictEntry) lookupResult {
	result := lookupResult{
		Word:   strings.TrimSpace(entry.Word),
		Source: "api.dictionaryapi.dev",
	}
	if result.Word == "" {
		result.Word = "word"
	}
	seenPhonetic := map[string]bool{}
	for _, p := range entry.Phonetics {
		text := strings.TrimSpace(p.Text)
		audio := strings.TrimSpace(p.Audio)
		if text == "" && audio == "" {
			continue
		}
		key := text + "\x00" + audio
		if seenPhonetic[key] {
			continue
		}
		seenPhonetic[key] = true
		result.Phonetics = append(result.Phonetics, lookupPhonetic{Text: text, Audio: audio})
	}
	for _, m := range entry.Meanings {
		meaning := lookupMeaning{PartOfSpeech: strings.TrimSpace(m.PartOfSpeech)}
		for i, d := range m.Definitions {
			if i >= lookupMaxDefinitions {
				break
			}
			def := lookupDefinition{
				Definition: strings.TrimSpace(d.Definition),
				Example:    strings.TrimSpace(d.Example),
				Synonyms:   truncateStrings(d.Synonyms, lookupMaxTerms),
				Antonyms: truncateStrings(d.Antonyms, lookupMaxTerms),
			}
			if def.Definition == "" {
				continue
			}
			meaning.Definitions = append(meaning.Definitions, def)
		}
		if len(meaning.Definitions) == 0 {
			continue
		}
		result.Meanings = append(result.Meanings, meaning)
	}
	result.Markdown = formatLookupMarkdown(result)
	return result
}

func truncateStrings(values []string, max int) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= max {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// formatLookupMarkdown produces a plain Markdown note body matching the
// shape of the lookup CLI output, ready to insert as a related document.
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
