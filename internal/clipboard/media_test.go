package clipboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeMediaFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	require.NoError(t, os.WriteFile(png, []byte("png-bytes"), 0o644))
	data, mime, err := decodeMediaFile(png)
	require.NoError(t, err)
	require.Equal(t, "image/png", mime)
	require.Equal(t, "png-bytes", string(data))

	mp4 := filepath.Join(dir, "clip.MP4")
	require.NoError(t, os.WriteFile(mp4, []byte("mp4-bytes"), 0o644))
	data, mime, err = decodeMediaFile(mp4)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", mime)
	require.Equal(t, "mp4-bytes", string(data))

	_, _, err = decodeMediaFile(filepath.Join(dir, "notes.txt"))
	require.ErrorIs(t, err, ErrEmpty)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644))
	_, _, err = decodeMediaFile(filepath.Join(dir, "notes.txt"))
	require.ErrorIs(t, err, ErrEmpty)
}
