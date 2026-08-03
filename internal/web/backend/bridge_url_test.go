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
