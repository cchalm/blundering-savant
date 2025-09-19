package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemDiffFileSystem_CreateFile(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	fs := NewMemDiffFileSystem(baseFS)

	err := fs.Write(ctx, "file1.txt", "file content")
	require.NoError(t, err)
	exists, err := fs.FileExists(ctx, "file1.txt")
	require.NoError(t, err)
	require.True(t, exists)
	content, err := fs.Read(ctx, "file1.txt")
	require.NoError(t, err)
	require.Equal(t, "file content", content)
}

func TestMemDiffFileSystem_OverwriteFile(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	err := baseFS.Write(ctx, "file1.txt", "file content 1")
	require.NoError(t, err)
	fs := NewMemDiffFileSystem(baseFS)

	err = fs.Write(ctx, "file1.txt", "file content 2")
	require.NoError(t, err)
	content, err := fs.Read(ctx, "file1.txt")
	require.NoError(t, err)
	require.Equal(t, "file content 2", content)
}

func TestMemDiffFileSystem_DeleteFile(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	err := baseFS.Write(ctx, "file1.txt", "file content")
	require.NoError(t, err)
	fs := NewMemDiffFileSystem(baseFS)

	err = fs.Delete(ctx, "file1.txt")
	require.NoError(t, err)
	exists, err := fs.FileExists(ctx, "file1.txt")
	require.NoError(t, err)
	require.False(t, exists)
	_, err = fs.Read(ctx, "file1.txt")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrFileNotFound)
}

func TestMemDiffFileSystem_DeleteNonExistentFile(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	fs := NewMemDiffFileSystem(baseFS)

	err := fs.Delete(ctx, "file1.txt")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrFileNotFound)
}

func TestMemDiffFileSystem_ListDir(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	baseFS.createDir("dir1", []string{"file1", "file2"})
	fs := NewMemDiffFileSystem(baseFS)

	{
		contents, err := fs.ListDir(ctx, "dir1")
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"file1", "file2"}, contents)
	}

	{
		// Add a file and test that it appears in the dir contents
		err := fs.Write(ctx, "dir1/file3", "file3 content")
		require.NoError(t, err)
		contents, err := fs.ListDir(ctx, "dir1")
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"file1", "file2", "dir1/file3"}, contents)
	}
}

func TestMemDiffFileSystem_ListDirNotExist(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	fs := NewMemDiffFileSystem(baseFS)

	_, err := fs.ListDir(ctx, "dir1")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrFileNotFound)
}

func TestMemDiffFileSystem_ListDirOnFile(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	fs := NewMemDiffFileSystem(baseFS)

	err := fs.Write(ctx, "file1.txt", "content")
	require.NoError(t, err)
	_, err = fs.ListDir(ctx, "file1.txt")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrIsFile)
}

func TestMemDiffFileSystem_IsDir(t *testing.T) {
	ctx := context.Background()
	baseFS := newFakeFS()
	baseFS.createDir("dir1", []string{"file1", "file2"})
	err := baseFS.Write(ctx, "dir2/file3", "file3 content")
	require.NoError(t, err)
	fs := NewMemDiffFileSystem(baseFS)

	{
		// Valid directory path
		isDir, err := fs.IsDir(ctx, "dir1")
		require.NoError(t, err)
		require.True(t, isDir)
	}

	{
		// Path that doesn't exist
		isDir, err := fs.IsDir(ctx, "dir2")
		require.NoError(t, err)
		require.False(t, isDir)
	}

	{
		// Path to a file
		isDir, err := fs.IsDir(ctx, "dir2/file3")
		require.NoError(t, err)
		require.False(t, isDir)
	}
}

// fakeFS is an in-memory file system implementation with fake directory behavior for testing
type fakeFS struct {
	files map[string]string
	dirs  map[string][]string
}

func newFakeFS() fakeFS {
	return fakeFS{
		files: map[string]string{},
		dirs:  map[string][]string{},
	}
}

func (ffs *fakeFS) createDir(dir string, contents []string) {
	ffs.dirs[dir] = contents
}

func (ffs fakeFS) Read(ctx context.Context, path string) (string, error) {
	if _, isDir := ffs.dirs[path]; isDir {
		return "", ErrIsDir
	}

	content, found := ffs.files[path]

	if !found {
		return "", ErrFileNotFound
	}

	return content, nil
}

func (ffs fakeFS) FileExists(ctx context.Context, path string) (bool, error) {
	_, found := ffs.files[path]
	return found, nil
}

func (ffs fakeFS) IsDir(ctx context.Context, dir string) (bool, error) {
	_, found := ffs.dirs[dir]
	return found, nil

}

func (ffs fakeFS) ListDir(ctx context.Context, dir string) ([]string, error) {
	contents, found := ffs.dirs[dir]
	if !found {
		return nil, ErrFileNotFound
	}

	return contents, nil
}

func (ffs *fakeFS) Write(ctx context.Context, path string, content string) error {
	ffs.files[path] = content
	return nil
}

func (ffs *fakeFS) Delete(ctx context.Context, path string) error {
	delete(ffs.files, path)
	return nil
}
