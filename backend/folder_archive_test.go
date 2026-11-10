package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeFolderArchiveIncludesEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "folder")
	os.MkdirAll(filepath.Join(src, "empty"), 0700)
	os.MkdirAll(filepath.Join(src, "nested"), 0700)
	os.WriteFile(filepath.Join(src, "nested", "hello.txt"), []byte("folder transfer"), 0600)
	dst := filepath.Join(root, "out.zip")
	if e := archiveDirectory(src, dst); e != nil {
		t.Fatal(e)
	}
	z, e := zip.OpenReader(dst)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name == "folder/empty/" {
			found = true
		}
		if f.Name == "folder/nested/hello.txt" {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil || string(b) != "folder transfer" {
				t.Fatal(e, string(b))
			}
		}
	}
	if !found {
		t.Fatal("empty directory missing")
	}
}
