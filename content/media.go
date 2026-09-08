package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrMediaDraftNotFound = errors.New("content draft not found")
	ErrMediaFinalized     = errors.New("media mutation denied for finalized draft")
	ErrUnsupportedImage   = errors.New("unsupported image")
)

type Media struct {
	ID, ContentDraftID              int64
	StoragePath, SHA256, UploadedBy string
	UploadedAt, DeletedAt           *time.Time
}
type MediaStore interface {
	AttachMedia(context.Context, int64, Media) (Media, *Media, error)
	CleanupRejectedMedia(context.Context, time.Time, bool) (int, error)
}

func MediaRootFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("URBAN_RADAR_MEDIA_DIR")); v != "" {
		return v
	}
	return "/var/lib/urban-radar/media"
}
func SaveImage(ctx context.Context, store MediaStore, root string, draftID int64, data []byte, actor string, now time.Time) (Media, error) {
	if draftID <= 0 || strings.TrimSpace(actor) == "" {
		return Media{}, ErrMediaDraftNotFound
	}
	_, format, e := image.Decode(bytes.NewReader(data))
	if e != nil || (format != "jpeg" && format != "png") {
		return Media{}, ErrUnsupportedImage
	}
	s := sha256.Sum256(data)
	h := hex.EncodeToString(s[:])
	ext := format
	if ext == "jpeg" {
		ext = "jpg"
	}
	rel := fmt.Sprintf("draft-%d-%s.%s", draftID, h[:16], ext)
	if e = os.MkdirAll(root, 0750); e != nil {
		return Media{}, e
	}
	f, e := os.CreateTemp(root, ".media-")
	if e != nil {
		return Media{}, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return Media{}, e
	}
	final := filepath.Join(root, rel)
	_, existed := os.Stat(final)
	if e = os.Rename(tmp, final); e != nil {
		return Media{}, e
	}
	m := Media{ContentDraftID: draftID, StoragePath: rel, SHA256: h, UploadedAt: &now, UploadedBy: actor}
	v, previous, e := store.AttachMedia(ctx, draftID, m)
	if e != nil {
		// A pre-existing name is the same deterministic hash and therefore the
		// existing attachment's exact bytes. A new name is an orphan on failure.
		if os.IsNotExist(existed) {
			_ = os.Remove(final)
		}
		return Media{}, e
	}
	if previous != nil && previous.StoragePath != v.StoragePath {
		_ = os.Remove(filepath.Join(root, previous.StoragePath))
	}
	return v, nil
}
