package backend

import "testing"

func TestNormalizeSourceURL_WeChatStripsQuery(t *testing.T) {
	raw := "https://mp.weixin.qq.com/s/prJYMuvhF44CaWte7JbyfA?scene=1&poc_token=abc#rd"
	got := normalizeSourceURL(raw)
	want := "https://mp.weixin.qq.com/s/prJYMuvhF44CaWte7JbyfA"
	if got != want {
		t.Fatalf("normalizeSourceURL = %q, want %q", got, want)
	}
}

func TestNormalizeSourceURL_OriginTrailingSlashIsStable(t *testing.T) {
	// Regression: page-clip idempotency keyed on source_url_norm. Origin URLs
	// with and without a trailing slash must collapse to one key.
	a := normalizeSourceURL("https://pi-from-scratch.vercel.app")
	b := normalizeSourceURL("https://pi-from-scratch.vercel.app/")
	want := "https://pi-from-scratch.vercel.app"
	if a != want || b != want {
		t.Fatalf("origin normalize: %q / %q, want %q", a, b, want)
	}
	if got := normalizeSourceURL("https://example.com/path/"); got != "https://example.com/path" {
		t.Fatalf("path trailing slash: %q", got)
	}
}

func TestSourceURLSearchKeys_IncludesArticleID(t *testing.T) {
	keys := sourceURLSearchKeys("https://mp.weixin.qq.com/s/prJYMuvhF44CaWte7JbyfA?scene=1")
	found := false
	for _, key := range keys {
		if key == "prJYMuvhF44CaWte7JbyfA" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected article id in keys, got %v", keys)
	}
}

func TestBodyCitesSourceURL_FrontMatter(t *testing.T) {
	body := "---\ntitle: \"x\"\nsource_url: \"https://mp.weixin.qq.com/s/prJYMuvhF44CaWte7JbyfA\"\n---\n\nhello\n"
	if !bodyCitesSourceURL(body, "https://mp.weixin.qq.com/s/prJYMuvhF44CaWte7JbyfA?scene=99") {
		t.Fatal("expected body to cite weixin source despite query mismatch")
	}
}
