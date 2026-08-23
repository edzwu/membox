package application

import "testing"

func TestAnnotationNoteKindsIncludeDurableGeneratedArtifacts(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", AnnotationNoteKindPlain},
		{"qa", AnnotationNoteKindQA},
		{"summary", AnnotationNoteKindSummary},
		{"translate", AnnotationNoteKindTranslation},
		{"translation", AnnotationNoteKindTranslation},
		{"jp_study", AnnotationNoteKindJPStudy},
		{"unknown", AnnotationNoteKindPlain},
	}
	for _, test := range tests {
		if got := NormalizeAnnotationNoteKind(test.input); got != test.want {
			t.Errorf("NormalizeAnnotationNoteKind(%q)=%q, want %q", test.input, got, test.want)
		}
	}
}

func TestDetectAnnotationNoteKindGeneratedMarkers(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{"**Q:** Why?\n\nBecause.", AnnotationNoteKindQA},
		{"**总结：**\n\n简短总结。", AnnotationNoteKindSummary},
		{"**翻译：**\n\n持久化译文。", AnnotationNoteKindTranslation},
		{"**语：**\n\n【翻译】内容", AnnotationNoteKindJPStudy},
		{"---\nkind: \"translation\"\n---\n\n译文", AnnotationNoteKindTranslation},
	}
	for _, test := range tests {
		if got := DetectAnnotationNoteKind(test.body); got != test.want {
			t.Errorf("DetectAnnotationNoteKind(%q)=%q, want %q", test.body, got, test.want)
		}
	}
}
