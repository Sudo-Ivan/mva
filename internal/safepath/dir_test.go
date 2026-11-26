package safepath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-version-archive/internal/vfs"
)

func TestDirRejectsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	paths := []string{
		outside,
		filepath.Join(root, "..", "outside.txt"),
		filepath.Join(root, "a", "..", "..", "outside.txt"),
		"/etc/passwd",
	}

	for _, p := range paths {
		if err := d.WriteFile(p, []byte("x"), FilePerm); err == nil {
			t.Errorf("WriteFile(%q) succeeded", p)
		}
		if _, err := d.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) succeeded", p)
		}
		if _, err := d.OpenFile(p); err == nil {
			t.Errorf("OpenFile(%q) succeeded", p)
		}
		if _, err := d.Stat(p); err == nil {
			t.Errorf("Stat(%q) succeeded", p)
		}
		if err := d.Remove(p); err == nil {
			t.Errorf("Remove(%q) succeeded", p)
		}
		if err := d.MkdirAll(p, DirPerm); err == nil {
			t.Errorf("MkdirAll(%q) succeeded", p)
		}
		if err := d.Rename(filepath.Join(root, "inside"), p); err == nil {
			t.Errorf("Rename to %q succeeded", p)
		}
	}

	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestDirRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	if err := os.MkdirAll(root, DirPerm); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), FilePerm); err != nil {
		t.Fatal(err)
	}

	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadFile(filepath.Join(root, "escape", "secret.txt")); err == nil {
		t.Fatal("read through escaping symlink succeeded")
	}
	if err := d.WriteFile(filepath.Join(root, "escape", "new.txt"), []byte("x"), FilePerm); err == nil {
		t.Fatal("write through escaping symlink succeeded")
	}
}

func TestDirReadWriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "nested", "file.txt")
	if err := d.WriteFile(path, []byte("hello"), FilePerm); err != nil {
		t.Fatal(err)
	}
	got, err := d.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	info, err := d.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != FilePerm {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	if !d.Exists(path) || d.Exists(filepath.Join(root, "missing")) {
		t.Fatal("Exists mismatch")
	}
	if err := d.Rename(path, filepath.Join(root, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove(filepath.Join(root, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	if d.Exists(filepath.Join(root, "moved.txt")) {
		t.Fatal("file still exists after Remove")
	}
}

func TestDirWriteFileExclusive(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "claim")
	if err := d.WriteFileExclusive(path, []byte("a"), FilePerm); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteFileExclusive(path, []byte("b"), FilePerm); err == nil {
		t.Fatal("second exclusive write succeeded")
	}
	got, err := d.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestDirRemoveAllOnRootKeepsRoot(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "sub/b.txt"} {
		if err := d.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("x"), FilePerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("root still has entries: %v", entries)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("root dir gone: %v", err)
	}
}

func TestDirWalkVisitsChildren(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "sub/b.txt"} {
		if err := d.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("x"), FilePerm); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	err = d.Walk(root, func(path string, entry vfs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "a.txt", "sub", "sub/b.txt"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	got := make(map[string]bool, len(seen))
	for _, p := range seen {
		got[p] = true
	}
	for _, p := range want {
		if !got[p] {
			t.Fatalf("seen = %v, missing %q", seen, p)
		}
	}
}

func TestDirCopyFromReaderLimit(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "copy.bin")
	n, err := d.CopyFromReader(path, strings.NewReader("0123456789"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("n = %d", n)
	}
	got, err := d.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123" {
		t.Fatalf("got %q", got)
	}
}

func TestReadFileAtAndOpenFileAt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "settings.ini")
	if err := os.WriteFile(path, []byte("key=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFileAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "key=value\n" {
		t.Fatalf("got %q", data)
	}
	f, err := OpenFileAt(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("key"))
	if _, err := f.Read(buf); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "key" {
		t.Fatalf("read %q", buf)
	}
	if _, err := ReadFileAt(filepath.Join(root, "missing", "file")); err == nil {
		t.Fatal("ReadFileAt on missing dir succeeded")
	}
	if _, err := ReadFileAt(root); err == nil {
		t.Fatal("ReadFileAt on a directory succeeded")
	}
}
