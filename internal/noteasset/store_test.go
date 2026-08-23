package noteasset

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 1x1 transparent PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f,
	0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d,
	0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestStoreContentAddressedNoteImage(t *testing.T) {
	home := t.TempDir()
	first, err := Store(home, bytes.NewReader(tinyPNG))
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaType != "image/png" || filepath.Ext(first.Filename) != ".png" || first.Size != int64(len(tinyPNG)) {
		t.Fatalf("image=%+v", first)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatal(err)
	}
	second, err := Store(home, bytes.NewReader(tinyPNG))
	if err != nil {
		t.Fatal(err)
	}
	if second.Path != first.Path || second.SHA256 != first.SHA256 {
		t.Fatalf("content-addressed store did not deduplicate: first=%+v second=%+v", first, second)
	}
	resolved, err := Resolve(home, first.Filename)
	if err != nil || resolved != first.Path {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func TestStorePathImportsAbsoluteImage(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(source, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	image, err := StorePath(home, source)
	if err != nil {
		t.Fatal(err)
	}
	if image.MediaType != "image/png" {
		t.Fatalf("image=%+v", image)
	}
	if _, err := StorePath(home, "relative.png"); err == nil {
		t.Fatal("relative source path was accepted")
	}
}

func TestStoreRejectsNonImageAndUnsafeName(t *testing.T) {
	if _, err := Store(t.TempDir(), bytes.NewBufferString("not an image")); err == nil {
		t.Fatal("non-image upload was accepted")
	}
	if _, err := Resolve(t.TempDir(), "../image.png"); err == nil {
		t.Fatal("unsafe image name was accepted")
	}
}
