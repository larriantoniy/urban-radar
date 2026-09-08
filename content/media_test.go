package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeMediaStore struct {
	current *Media
	status  string
	fail    bool
}

func (s *fakeMediaStore) AttachMedia(_ context.Context, id int64, m Media) (Media, *Media, error) {
	if s.fail {
		return Media{}, nil, errors.New("db failed")
	}
	if s.status != "PENDING" {
		return Media{}, nil, ErrMediaFinalized
	}
	old := s.current
	s.current = &m
	return m, old, nil
}
func (s *fakeMediaStore) CleanupRejectedMedia(context.Context, time.Time, bool) (int, error) {
	return 0, nil
}
func imageBytes(t *testing.T, value uint8) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: value, A: 255})
	if e := png.Encode(&buffer, img); e != nil {
		t.Fatal(e)
	}
	return buffer.Bytes()
}
func TestSaveImageCreatesRelativeHashedMedia(t *testing.T) {
	root := t.TempDir()
	s := &fakeMediaStore{status: "PENDING"}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bytes := imageBytes(t, 1)
	m, e := SaveImage(context.Background(), s, root, 7, bytes, "telegram:1", now)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.IsAbs(m.StoragePath) || m.SHA256 == "" {
		t.Fatalf("bad media %+v", m)
	}
	want := sha256.Sum256(bytes)
	if m.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256=%q", m.SHA256)
	}
	if _, e = os.Stat(filepath.Join(root, m.StoragePath)); e != nil {
		t.Fatal(e)
	}
}
func TestSaveImageReplacementAndFailedReplacementKeepsPrevious(t *testing.T) {
	root := t.TempDir()
	s := &fakeMediaStore{status: "PENDING"}
	first, e := SaveImage(context.Background(), s, root, 7, imageBytes(t, 1), "telegram:1", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	s.fail = true
	if _, e = SaveImage(context.Background(), s, root, 7, imageBytes(t, 2), "telegram:1", time.Now()); e == nil {
		t.Fatalf("got %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, first.StoragePath)); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, e)
	}
	s.fail = false
	second, e := SaveImage(context.Background(), s, root, 7, imageBytes(t, 2), "telegram:1", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if second.StoragePath == first.StoragePath {
		t.Fatal("replacement reused a different image path")
	}
	if _, e = os.Stat(filepath.Join(root, first.StoragePath)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("old file err=%v", e)
	}
}
func TestSaveImageRejectsFinalizedAndUnsupported(t *testing.T) {
	s := &fakeMediaStore{status: "APPROVED"}
	if _, e := SaveImage(context.Background(), s, t.TempDir(), 7, imageBytes(t, 1), "telegram:1", time.Now()); !errors.Is(e, ErrMediaFinalized) {
		t.Fatalf("got %v", e)
	}
	if _, e := SaveImage(context.Background(), s, t.TempDir(), 7, []byte("no"), "telegram:1", time.Now()); !errors.Is(e, ErrUnsupportedImage) {
		t.Fatalf("got %v", e)
	}
}
