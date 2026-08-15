package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestShapeLookupResultMarkdown(t *testing.T) {
	raw := []byte(`[
	  {
	    "word": "hello",
	    "phonetics": [
	      {"text": "/həˈloʊ/", "audio": "https://example.com/a.mp3"},
	      {"text": "/həˈloʊ/", "audio": ""}
	    ],
	    "meanings": [
	      {
	        "partOfSpeech": "noun",
	        "definitions": [
	          {
	            "definition": "A greeting.",
	            "example": "Hello there!",
	            "synonyms": ["hi", "hey"]
	          }
	        ]
	      }
	    ]
	  }
	]`)
	var entries []freeDictEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	result := shapeLookupResult(entries[0])
	if result.Word != "hello" || len(result.Meanings) != 1 || len(result.Phonetics) != 2 {
		t.Fatalf("unexpected shape: %+v", result)
	}
	for _, needle := range []string{
		"# hello",
		"## noun",
		"A greeting.",
		"syn: hi, hey",
		"source: api.dictionaryapi.dev",
	} {
		if !strings.Contains(result.Markdown, needle) {
			t.Fatalf("markdown missing %q:\n%s", needle, result.Markdown)
		}
	}
}

func TestHandleLookupRejectsInvalidWord(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/lookup?q=two+words", nil)
	server.handleLookup(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
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

func TestHandleLookupSuccessWithFakeTransport(t *testing.T) {
	original := dictionaryHTTPClient
	originalSleep := dictionarySleep
	lookupCacheReset()
	t.Cleanup(func() {
		dictionaryHTTPClient = original
		dictionarySleep = originalSleep
		lookupCacheReset()
	})
	dictionarySleep = func(time.Duration) {}

	payload := `[
	  {
	    "word": "serendipity",
	    "phonetics": [{"text": "/ˌser.ənˈdɪp.ə.ti/"}],
	    "meanings": [
	      {
	        "partOfSpeech": "noun",
	        "definitions": [{"definition": "A happy accident."}]
	      }
	    ]
	  }
	]`
	dictionaryHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/serendipity") {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}

	server := &Server{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/lookup?q=Serendipity!", nil)
	server.handleLookup(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var result lookupResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Word != "serendipity" || !strings.Contains(result.Markdown, "A happy accident.") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFetchDictionaryRetriesTransientUpstreamErrors(t *testing.T) {
	original := dictionaryHTTPClient
	originalSleep := dictionarySleep
	lookupCacheReset()
	t.Cleanup(func() {
		dictionaryHTTPClient = original
		dictionarySleep = originalSleep
		lookupCacheReset()
	})
	dictionarySleep = func(time.Duration) {}

	payload := `[{"word":"hello","meanings":[{"partOfSpeech":"noun","definitions":[{"definition":"A greeting."}]}]}]`
	attempts := 0
	dictionaryHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}

	result, status, err := fetchDictionary(context.Background(), "hello")
	if err != nil || status != http.StatusOK || result.Word != "hello" {
		t.Fatalf("result=%+v status=%d err=%v attempts=%d", result, status, err, attempts)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d, want 3", attempts)
	}

	// Cache should satisfy a second call without another upstream hit.
	result, status, err = fetchDictionary(context.Background(), "hello")
	if err != nil || status != http.StatusOK || result.Word != "hello" || attempts != 3 {
		t.Fatalf("cached call failed: result=%+v status=%d err=%v attempts=%d", result, status, err, attempts)
	}
}

func TestFetchDictionaryDoesNotRetryNotFound(t *testing.T) {
	original := dictionaryHTTPClient
	originalSleep := dictionarySleep
	lookupCacheReset()
	t.Cleanup(func() {
		dictionaryHTTPClient = original
		dictionarySleep = originalSleep
		lookupCacheReset()
	})
	dictionarySleep = func(time.Duration) {}
	attempts := 0
	dictionaryHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"title":"No Definitions Found"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	_, status, err := fetchDictionary(context.Background(), "xyzzynotaword")
	if status != http.StatusNotFound || err == nil || attempts != 1 {
		t.Fatalf("status=%d err=%v attempts=%d", status, err, attempts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
