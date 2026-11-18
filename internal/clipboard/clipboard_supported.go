//go:build (darwin || linux || windows || freebsd || openbsd || netbsd) && !ios && !android

package clipboard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.design/x/clipboard"
)

// A clipboard is shared with every other program on the machine, and a write
// is not one atomic step: the backend clears the clipboard and then sets the
// new contents. Another writer landing in that window makes a perfectly good
// write look lost, so try a few times before believing it.
const (
	writeAttempts = 3
	retryDelay    = 5 * time.Millisecond
)

// ready reports whether the native clipboard is usable. Touching the clipboard
// after a failed initialization may panic, and golang.design's Init is
// idempotent and cheap once it has run, so every entry point asks it again
// rather than tracking initialization state of our own.
func ready() bool {
	return clipboard.Init() == nil
}

func initClipboard() error {
	return clipboard.Init()
}

func writeText(text string) error {
	if !ready() {
		return ErrUnsupported
	}
	var err error
	for attempt := range writeAttempts {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		if err = attemptWriteText(text); err == nil {
			return nil
		}
	}
	return err
}

func attemptWriteText(text string) error {
	// A write error means the backend never took the clipboard; reading back
	// catches the rest, where the write is accepted but the text is not served
	// afterwards. Neither check subsumes the other: a failed write leaves an
	// earlier identical copy in place, which reads back as a success.
	if _, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(text)); err != nil {
		return ErrWriteFailed
	}
	// Writing nothing empties the clipboard, so there is nothing to read back.
	if text == "" {
		return nil
	}
	data, err := clipboard.Read(context.Background(), clipboard.FmtText)
	if err != nil || !bytes.Equal(data, []byte(text)) {
		return ErrWriteFailed
	}
	return nil
}

func read(f Format) ([]byte, error) {
	if !ready() {
		return nil, ErrUnsupported
	}
	var format clipboard.Format
	switch f {
	case FormatText:
		format = clipboard.FmtText
	case FormatImage:
		format = clipboard.FmtImage
	default:
		return nil, ErrEmpty
	}
	data, err := clipboard.Read(context.Background(), format)
	if err != nil || data == nil {
		return nil, ErrEmpty
	}
	return data, nil
}

// clipMediaTypes lists, in preference order, the clipboard MIME types that
// carry image and video bytes. Desktops advertise what they have in
// clipboard.Formats, so a video needs no format of our own: it is read as the
// bytes the clipboard already holds.
var clipMediaTypes = []string{
	"image/png",
	"image/jpeg",
	"image/webp",
	"image/gif",
	"video/mp4",
	"video/webm",
	"video/quicktime",
	"video/x-matroska",
}

// readMedia reads the first image or video payload on the clipboard, falling
// back to a file path copied from a file manager.
func readMedia() (Media, error) {
	if !ready() {
		return Media{}, ErrUnsupported
	}
	ctx := context.Background()

	// A copied file is a path, not bytes. Prefer the file itself: it keeps the
	// real container for a video the desktop would not serve as raw data.
	if paths, err := clipboard.ReadFiles(ctx); err == nil && len(paths) > 0 {
		if data, mime, err := decodeMediaFile(paths[0]); err == nil {
			return Media{Data: data, MIMEType: mime}, nil
		}
	}

	available, err := clipboard.Formats(ctx)
	if err != nil {
		return Media{}, ErrEmpty
	}
	present := make(map[string]bool, len(available))
	for _, f := range available {
		present[f.MIME()] = true
	}
	for _, mime := range clipMediaTypes {
		if !present[mime] {
			continue
		}
		data, err := clipboard.Read(ctx, clipboard.Register(mime))
		if err != nil || len(data) == 0 {
			continue
		}
		return Media{Data: data, MIMEType: mime}, nil
	}

	// The desktop may advertise a type we do not list; fall back to the
	// built-in image format, which transcodes whatever the desktop holds.
	data, err := clipboard.Read(ctx, clipboard.FmtImage)
	if err != nil || len(data) == 0 {
		return Media{}, ErrEmpty
	}
	return Media{Data: data, MIMEType: "image/png"}, nil
}

// decodeMediaFile reads a copied file when it is media we can accept, and
// reports the MIME type to label it with. A video container is identified by
// extension because its header is not reliably sniffable.
func decodeMediaFile(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, "", ErrEmpty
	}
	ext := strings.ToLower(filepath.Ext(path))
	var mime string
	switch ext {
	case ".png":
		mime = "image/png"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	case ".gif":
		mime = "image/gif"
	case ".mp4", ".m4v":
		mime = "video/mp4"
	case ".webm":
		mime = "video/webm"
	case ".mov":
		mime = "video/quicktime"
	case ".mkv":
		mime = "video/x-matroska"
	default:
		return nil, "", ErrEmpty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", ErrEmpty
	}
	return data, mime, nil
}
