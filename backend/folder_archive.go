package main

import (
	"archive/zip"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type folderArchive struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
	path  string
}

func archiveDirectory(root, target string) error {
	f, e := os.Create(target)
	if e != nil {
		return e
	}
	z := zip.NewWriter(f)
	e = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Clean(path), filepath.Clean(target)) {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("文件夹包含符号链接，请移除链接后重试")
		}
		rel, err := filepath.Rel(filepath.Dir(root), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") {
			return errors.New("文件夹路径越界")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return errors.New("文件夹包含不支持的特殊文件")
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Method = zip.Store
		if d.IsDir() {
			h.Name += "/"
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	ce := z.Close()
	fe := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return fe
}

func (r *imRuntime) handleArchive(method string, params json.RawMessage) (any, error) {
	if method == "im/archive-pick" {
		dir, e := pickDirectory()
		if e != nil {
			return nil, e
		}
		if dir == "" {
			return map[string]any{"cancelled": true}, nil
		}
		b := make([]byte, 24)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		f, e := os.CreateTemp("", "neighbor-folder-*.zip")
		if e != nil {
			return nil, e
		}
		f.Close()
		job := &folderArchive{ID: hex.EncodeToString(b), Name: filepath.Base(dir) + ".zip", path: f.Name()}
		r.mu.Lock()
		if r.archives == nil {
			r.archives = map[string]*folderArchive{}
		}
		r.archives[job.ID] = job
		id := job.ID
		r.mu.Unlock()
		go func() {
			err := archiveDirectory(dir, job.path)
			var size int64
			if err == nil {
				var st os.FileInfo
				st, err = os.Stat(job.path)
				if err == nil {
					size = st.Size()
				}
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			job.Ready = true
			job.Size = size
			if err != nil {
				job.Error = err.Error()
				_ = os.Remove(job.path)
			}
		}()
		return map[string]string{"id": id}, nil
	}
	var a struct {
		ID     string `json:"id"`
		Offset int64  `json:"offset"`
	}
	if e := json.Unmarshal(params, &a); e != nil {
		return nil, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.archives[a.ID]
	if job == nil {
		return nil, errors.New("文件夹任务不存在")
	}
	if method == "im/archive-status" {
		return *job, nil
	}
	if !job.Ready {
		return nil, errors.New("文件夹正在准备")
	}
	if method == "im/archive-release" {
		delete(r.archives, a.ID)
		_ = os.Remove(job.path)
		return map[string]bool{"ok": true}, nil
	}
	if job.Error != "" {
		return nil, errors.New(job.Error)
	}
	if a.Offset < 0 || a.Offset > job.Size {
		return nil, errors.New("无效文件偏移")
	}
	f, e := os.Open(job.path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	buf := make([]byte, 262144)
	n, e := f.ReadAt(buf, a.Offset)
	if e != nil && e != io.EOF {
		return nil, e
	}
	return map[string]string{"dataBase64": base64.StdEncoding.EncodeToString(buf[:n])}, nil
}
