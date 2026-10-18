package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"xbot/internal/limits"
)

// Dir 是這台電腦上的一個資料夾當成 /workspace（測試、沒有 Docker 時用）。用 os.Root：任何寫法都出不了這個資料夾；
// 另外和 Docker 版一樣，路徑經過連結一律拒絕。
type Dir struct{ Path string }

// DirLocator 把每個 Bot 的 /workspace 放在 root/<botID>/workspace。
func DirLocator(root string) Locator {
	return func(_ context.Context, botID string, _ bool) (Workspace, error) {
		p := path.Join(root, botID, "workspace")
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		return Dir{Path: p}, nil
	}
}

func (d Dir) open() (*os.Root, error) { return os.OpenRoot(d.Path) }

func name(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// check 一段一段看路徑：經過連結回 ErrLink；不存在時，allowMissing 為 true 回傳存在的那幾段，否則回 ErrNotFound。
func (d Dir) check(r *os.Root, rel string, allowMissing bool) (fs.FileInfo, int, error) {
	parts := split(rel)
	fi, err := r.Lstat(".")
	if err != nil {
		return nil, 0, err
	}
	for i := range parts {
		fi, err = r.Lstat(strings.Join(parts[:i+1], "/"))
		if errors.Is(err, fs.ErrNotExist) {
			if allowMissing {
				return nil, i, nil
			}
			return nil, i, ErrNotFound
		}
		if err != nil {
			return nil, i, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, i, ErrLink
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return nil, i, ErrNotDir
		}
	}
	return fi, len(parts), nil
}

func infoOf(rel string, fi fs.FileInfo) Info {
	return Info{Rel: rel, Dir: fi.IsDir(), Size: fi.Size(), Modified: fi.ModTime()}
}

func join(dir, n string) string {
	if dir == "" {
		return n
	}
	return dir + "/" + n
}

// List 實作 Workspace。
func (d Dir) List(_ context.Context, rel string) ([]Info, error) {
	r, err := d.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	fi, _, err := d.check(r, rel, false)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, ErrNotDir
	}
	f, err := r.Open(name(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ents, err := f.ReadDir(limits.FilesFolderScanMax)
	if err != nil && err != io.EOF {
		return nil, err
	}
	out := make([]Info, 0, len(ents))
	for _, e := range ents {
		if e.Type()&fs.ModeSymlink != 0 || (!e.IsDir() && !e.Type().IsRegular()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, infoOf(join(rel, e.Name()), fi))
	}
	return out, nil
}

// Walk 實作 Workspace。
func (d Dir) Walk(_ context.Context) ([]Info, error) {
	r, err := d.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []Info
	err = fs.WalkDir(r.FS(), ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") || (e.IsDir() && pruneDirs[strings.ToLower(e.Name())]) {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return nil
		}
		out = append(out, infoOf(p, fi))
		if len(out) >= limits.FilesWalkScanMax {
			return fs.SkipAll
		}
		return nil
	})
	return out, err
}

// Stat 實作 Workspace。
func (d Dir) Stat(_ context.Context, rel string) (Info, error) {
	r, err := d.open()
	if err != nil {
		return Info{}, err
	}
	defer r.Close()
	fi, _, err := d.check(r, rel, false)
	if err != nil {
		return Info{}, err
	}
	return infoOf(rel, fi), nil
}

type rootFile struct {
	*os.File
	root *os.Root
}

func (f rootFile) Close() error {
	err := f.File.Close()
	f.root.Close()
	return err
}

// Open 實作 Workspace。
func (d Dir) Open(_ context.Context, rel string) (io.ReadCloser, Info, error) {
	r, err := d.open()
	if err != nil {
		return nil, Info{}, err
	}
	fi, _, err := d.check(r, rel, false)
	if err == nil && fi.IsDir() {
		err = ErrIsDir
	}
	if err == nil && !fi.Mode().IsRegular() {
		err = ErrNotFound
	}
	if err != nil {
		r.Close()
		return nil, Info{}, err
	}
	f, err := r.OpenFile(rel, os.O_RDONLY|nofollow, 0)
	if err != nil {
		r.Close()
		return nil, Info{}, err
	}
	// 開了以後再看一次：檢查和開啟之間被換成別的東西就不給。
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		r.Close()
		return nil, Info{}, ErrNotFound
	}
	return rootFile{File: f, root: r}, infoOf(rel, fi), nil
}

// Write 實作 Workspace。
func (d Dir) Write(_ context.Context, rel string, size int64, src io.Reader) error {
	if rel == "" {
		return ErrIsDir
	}
	r, err := d.open()
	if err != nil {
		return err
	}
	defer r.Close()
	fi, _, err := d.check(r, rel, true)
	if err != nil {
		return err
	}
	if fi != nil && fi.IsDir() {
		return ErrIsDir
	}
	if dir := path.Dir(rel); dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|nofollow, 0o644)
	if err != nil {
		return err
	}
	n, err := io.CopyN(f, src, size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == io.EOF {
		err = fmt.Errorf("內容不完整（%d／%d 位元組）", n, size)
	}
	return err
}

// Mkdir 實作 Workspace。
func (d Dir) Mkdir(_ context.Context, rel string) error {
	r, err := d.open()
	if err != nil {
		return err
	}
	defer r.Close()
	fi, _, err := d.check(r, rel, true)
	if err != nil {
		return err
	}
	if fi != nil {
		if fi.IsDir() {
			return nil
		}
		return ErrExists
	}
	return r.MkdirAll(rel, 0o755)
}

// Move 實作 Workspace。
func (d Dir) Move(_ context.Context, from, to string) error {
	r, err := d.open()
	if err != nil {
		return err
	}
	defer r.Close()
	if _, _, err := d.check(r, path.Dir(from), false); err != nil && path.Dir(from) != "." {
		return err
	}
	if _, err := r.Lstat(from); err != nil {
		return ErrNotFound
	}
	fi, _, err := d.check(r, to, true)
	if err != nil {
		return err
	}
	if fi != nil {
		return ErrExists
	}
	if dir := path.Dir(to); dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return r.Rename(from, to)
}

// Remove 實作 Workspace。
func (d Dir) Remove(_ context.Context, rel string) error {
	if rel == "" {
		return errors.New("不能刪除整個工作目錄")
	}
	r, err := d.open()
	if err != nil {
		return err
	}
	defer r.Close()
	if dir := path.Dir(rel); dir != "." {
		if _, _, err := d.check(r, dir, false); err != nil {
			return err
		}
	}
	if _, err := r.Lstat(rel); err != nil {
		return ErrNotFound
	}
	return r.RemoveAll(rel)
}
