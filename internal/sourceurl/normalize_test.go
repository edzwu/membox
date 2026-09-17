package sourceurl

import "testing"

func TestNormalizeShareAndTrackingVariants(t *testing.T) {
	tests := map[string]string{
		"https://x.com/maharshii/status/2100193318277890174?s=12":                   "https://x.com/maharshii/status/2100193318277890174",
		"https://twitter.com/maharshii/status/2100193318277890174?t=abc&s=19#reply": "https://twitter.com/maharshii/status/2100193318277890174",
		"https://mp.weixin.qq.com/s/article-id?scene=1&poc_token=abc#rd":            "https://mp.weixin.qq.com/s/article-id",
		"https://example.com/article?id=42&utm_source=test#part":                    "https://example.com/article?id=42&utm_source=test",
	}
	for raw, want := range tests {
		if got := Normalize(raw); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizeYouTubeAndTrailingSlashes(t *testing.T) {
	tests := map[string]string{
		"https://youtu.be/dQw4w9WgXcQ?t=12":                     "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLxx": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://example.com/":                                  "https://example.com",
		"https://example.com/a/b/":                              "https://example.com/a/b",
	}
	for raw, want := range tests {
		if got := Normalize(raw); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", raw, got, want)
		}
	}
}
