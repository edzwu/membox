package remotedocs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDetectType(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"paper.pdf", []byte("%PDF-1.7\n"), "application/pdf"},
		{"notes.md", []byte("# hello\n"), "text/markdown"},
		{"notes.markdown", []byte("# hello\n"), "text/markdown"},
	}
	for _, tc := range cases {
		if got, err := DetectType(tc.name, tc.head); err != nil || got != tc.want {
			t.Errorf("DetectType(%q): got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	for _, bad := range []struct {
		name string
		head []byte
	}{
		{"evil.pdf", []byte("# not a pdf")},     // extension lies, magic wins
		{"binary.md", []byte{0x00, 0x01, 0x02}}, // NUL bytes → not text
		{"page.html", []byte("<html>")},         // stage 1: md/pdf only
	} {
		if _, err := DetectType(bad.name, bad.head); err == nil {
			t.Errorf("DetectType(%q): want rejection", bad.name)
		}
	}
}

// fakeRepo implements Repository in memory for service-level tests.
type fakeRepo struct {
	docs     map[string]Document
	versions map[string][]Version
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{docs: map[string]Document{}, versions: map[string][]Version{}}
}

func (f *fakeRepo) Create(_ context.Context, doc Document, v Version) error {
	f.docs[doc.ID] = doc
	f.versions[doc.ID] = append(f.versions[doc.ID], v)
	return nil
}

func (f *fakeRepo) List(_ context.Context, q ListQuery) (ListResult, error) {
	var result ListResult
	for _, doc := range f.docs {
		if doc.OwnerID == q.OwnerID && !doc.Deleted {
			result.Documents = append(result.Documents, doc)
		}
	}
	return result, nil
}

func (f *fakeRepo) Get(_ context.Context, ownerID, id string) (Document, error) {
	doc, ok := f.docs[id]
	if !ok || doc.OwnerID != ownerID || doc.Deleted {
		return Document{}, ErrNotFound
	}
	return doc, nil
}

func (f *fakeRepo) VersionAt(_ context.Context, ownerID, id string, rev int) (Version, error) {
	if _, err := f.Get(context.Background(), ownerID, id); err != nil {
		return Version{}, err
	}
	versions := f.versions[id]
	if rev > 0 {
		for _, v := range versions {
			if v.Revision == rev {
				return v, nil
			}
		}
		return Version{}, ErrNotFound
	}
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].BlobKey != "" {
			return versions[i], nil
		}
	}
	return Version{}, ErrNotFound
}

func (f *fakeRepo) Update(_ context.Context, ownerID, id string, expectedRev int, deleted bool, v Version) error {
	doc, err := f.Get(context.Background(), ownerID, id)
	if err != nil {
		return err
	}
	if doc.CurrentRevision != expectedRev {
		return ErrRevisionConflict
	}
	doc.CurrentRevision = expectedRev + 1
	doc.Deleted = deleted
	doc.Title = v.Title
	doc.SourceURL = v.SourceURL
	f.docs[id] = doc
	f.versions[id] = append(f.versions[id], v)
	return nil
}

type fakeBlobs struct{ objects map[string][]byte }

func (f *fakeBlobs) Put(_ context.Context, key string, body io.Reader, _ int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[key] = data
	return nil
}

func (f *fakeBlobs) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

var testIDs = func() func() (string, error) {
	n := 0
	return func() (string, error) {
		n++
		return strings.Repeat("0", 35) + string(rune('a'+n-1)), nil
	}
}()

func newTestService() (*Service, *fakeRepo, *fakeBlobs) {
	repo := newFakeRepo()
	blobs := &fakeBlobs{}
	return NewService(repo, blobs, testIDs, 1<<20), repo, blobs
}

func TestCreateValidatesHashesAndVersions(t *testing.T) {
	svc, repo, blobs := newTestService()
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 100)...)
	doc, err := svc.Create(context.Background(), "owner", "paper.pdf", "https://x/p.pdf", bytes.NewReader(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if doc.CurrentRevision != 1 {
		t.Fatalf("revision: got %d", doc.CurrentRevision)
	}
	version, err := repo.VersionAt(context.Background(), "owner", doc.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if version.Operation != OpCreate || version.SizeBytes != int64(len(pdf)) {
		t.Fatalf("bad v1: %+v", version)
	}
	if got := blobs.objects[version.BlobKey]; !bytes.Equal(got, pdf) {
		t.Fatal("blob bytes differ from upload")
	}
}

func TestCreateRejectsNonPDF(t *testing.T) {
	svc, _, _ := newTestService()
	if _, err := svc.Create(context.Background(), "owner", "run.sh", "", strings.NewReader("#!/bin/sh")); err == nil {
		t.Fatal("want rejection for shell script")
	}
}

func TestCASConflict(t *testing.T) {
	svc, _, _ := newTestService()
	doc, _ := svc.Create(context.Background(), "owner", "a.md", "", strings.NewReader("# a"))
	if _, err := svc.UpdateMeta(context.Background(), "owner", doc.ID, 1, "new", ""); err != nil {
		t.Fatal(err)
	}
	// Replaying rev-1 must conflict.
	if _, err := svc.UpdateMeta(context.Background(), "owner", doc.ID, 1, "again", ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestSoftDeleteHidesAndKeepsBlob(t *testing.T) {
	svc, _, blobs := newTestService()
	doc, _ := svc.Create(context.Background(), "owner", "a.md", "", strings.NewReader("# a"))
	if err := svc.SoftDelete(context.Background(), "owner", doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), "owner", doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted doc should be invisible, got %v", err)
	}
	if len(blobs.objects) != 1 {
		t.Fatal("blob must be retained after soft delete")
	}
}

func TestOwnerIsolation(t *testing.T) {
	svc, _, _ := newTestService()
	doc, _ := svc.Create(context.Background(), "owner-a", "a.md", "", strings.NewReader("# a"))
	if _, err := svc.Get(context.Background(), "owner-b", doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign doc must be NotFound, got %v", err)
	}
}
