package pdfasset

import (
	"path/filepath"
	"testing"
)

func TestRootAndImageTarget(t *testing.T) {
	root, err := Root("/library/book.pdf", "019ffe54-a513-7da8-bfac-0ae403223ccf")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join("/library", DirectoryName, "019ffe54-a513-7da8-bfac-0ae403223ccf")
	if root != wantRoot {
		t.Fatalf("root=%q want=%q", root, wantRoot)
	}
	target, err := ImageTarget(root, "images/chart.jpg")
	if err != nil || target != filepath.Join(wantRoot, "images", "chart.jpg") {
		t.Fatalf("target=%q err=%v", target, err)
	}
	noteRoot, err := Directory("/library", "01a0177c-48da-79cb-8b1c-46f435a660b4")
	if err != nil {
		t.Fatal(err)
	}
	wantNote := filepath.Join("/library", DirectoryName, "01a0177c-48da-79cb-8b1c-46f435a660b4")
	if noteRoot != wantNote {
		t.Fatalf("note root=%q want=%q", noteRoot, wantNote)
	}
}

func TestImageTargetRejectsUnsafeAndNonImagePaths(t *testing.T) {
	for _, path := range []string{"../x.jpg", "images/../../x.jpg", "/images/x.jpg", "images/nested/x.jpg", "images/x.txt"} {
		if _, err := ImageTarget("/assets", path); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
}
