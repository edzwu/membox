package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNormalizeLookupWord(t *testing.T) {
	cases := map[string]string{
		"Hello":                 "hello",
		"  serendipity. ":       "serendipity",
		"well-known":            "well-known",
		"don't":                 "don't",
		"two words":             "",
		"":                      "",
		"123":                   "",
		"你好":                    "",
		strings.Repeat("a", 41): "",
	}
	for input, want := range cases {
		if got := normalizeLookupWord(input); got != want {
			t.Fatalf("normalizeLookupWord(%q)=%q, want %q", input, got, want)
		}
	}
}

func setupTestGdict(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dictionary.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE en (
  word TEXT,
  pos TEXT,
  sounds TEXT,
  etymology_text TEXT,
  senses TEXT
);
CREATE INDEX idx_en_word ON en(word);
INSERT INTO en(word, pos, sounds, senses) VALUES
(
  'serendipity',
  'noun',
  '[{"ipa":"/ˌser.ənˈdɪp.ə.ti/","tags":["UK"]}]',
  '[{"glosses":["noun","A happy accident."],"examples":[{"text":"What serendipity!"}],"synonyms":[{"word":"fluke"}],"antonyms":[]}]'
),
(
  'hello',
  'intj',
  '[{"ipa":"/həˈloʊ/"}]',
  '[{"glosses":["A greeting."],"examples":[{"text":"Hello there!"}]}]'
);
`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFetchDictionaryLocal(t *testing.T) {
	path := setupTestGdict(t)
	old := gdictDBPath
	gdictDBPath = func() string { return path }
	t.Cleanup(func() {
		gdictDBPath = old
		closeGdictDB()
		lookupCacheReset()
	})
	closeGdictDB()
	lookupCacheReset()

	result, status, err := fetchDictionary(context.Background(), "serendipity", "en")
	if err != nil || status != http.StatusOK {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if result.Word != "serendipity" || len(result.Meanings) == 0 {
		t.Fatalf("%+v", result)
	}
	if !strings.Contains(result.Markdown, "A happy accident") {
		t.Fatalf("markdown=%s", result.Markdown)
	}
	if result.Source != "gdict (wiktionary offline)" {
		t.Fatalf("source=%q", result.Source)
	}
	// cache hit
	result2, _, err := fetchDictionary(context.Background(), "serendipity", "en")
	if err != nil || result2.Word != "serendipity" {
		t.Fatalf("cache: %+v err=%v", result2, err)
	}

	_, status, err = fetchDictionary(context.Background(), "xyzzynotaword", "en")
	if status != http.StatusNotFound || err == nil {
		t.Fatalf("want 404, status=%d err=%v", status, err)
	}
}

func TestHandleLookupLocal(t *testing.T) {
	path := setupTestGdict(t)
	old := gdictDBPath
	gdictDBPath = func() string { return path }
	t.Cleanup(func() {
		gdictDBPath = old
		closeGdictDB()
		lookupCacheReset()
	})
	closeGdictDB()
	lookupCacheReset()

	server := &Server{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/lookup?q=Hello!", nil)
	server.handleLookup(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var result lookupResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Word != "hello" || !strings.Contains(result.Markdown, "greeting") {
		t.Fatalf("%+v", result)
	}

	bad := httptest.NewRecorder()
	server.handleLookup(bad, httptest.NewRequest(http.MethodGet, "/api/lookup?q=two+words", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", bad.Code)
	}
}

func TestHandleLookupMethodNotAllowed(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/lookup?q=hello", nil)
	server.handleLookup(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestParseGdictSensesSkipsHeaderGloss(t *testing.T) {
	raw := `[{"glosses":["noun","A happy accident."],"examples":[{"text":"x"}]}]`
	defs := parseGdictSenses(raw)
	if len(defs) != 1 || defs[0].Definition != "A happy accident." || defs[0].Example != "x" {
		t.Fatalf("%+v", defs)
	}
}
