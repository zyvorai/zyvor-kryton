// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package dockur

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// reservedDir holds Kryton's own job protocol files inside the shared
// folder; the generic file API refuses to touch it.
const reservedDir = ".kryton"

const maxListEntries = 5000

// resolveIn maps a slash-separated relative path onto root. It rejects
// anything ValidateRelPath rejects, the reserved .kryton directory (unless
// allowReserved), and any path whose existing components include a
// symlink: the guest can write the shared folder and could otherwise plant
// a link that makes the host follow it out of the machine directory.
// An empty rel resolves to root itself.
func resolveIn(root, rel string, allowReserved bool) (string, error) {
	if rel == "" {
		return root, nil
	}
	if err := model.ValidateRelPath(rel, true); err != nil {
		return "", errf(provider.ErrInvalid, "%v", err)
	}
	if !allowReserved && strings.SplitN(rel, "/", 2)[0] == reservedDir {
		return "", errf(provider.ErrInvalid, "%s is reserved for Kryton", reservedDir)
	}
	if rootFI, err := os.Lstat(root); err != nil || !rootFI.IsDir() {
		return "", errf(provider.ErrPrecondition, "shared folder is missing")
	}
	cur := root
	for _, seg := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				// Remaining components do not exist yet; nothing to follow.
				full := filepath.Join(root, filepath.FromSlash(rel))
				if !within(root, full) {
					return "", errf(provider.ErrInvalid, "path escapes the shared folder")
				}
				return full, nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errf(provider.ErrInvalid, "path traverses a symbolic link")
		}
	}
	return cur, nil
}

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func (p *Provider) sharedRoot(mi machineInfo) (string, error) {
	if mi.lay.Shared == "" {
		return "", errf(provider.ErrPrecondition, "machine was created without a shared folder (dockur.shared)")
	}
	return filepath.Join(mi.dir, mi.lay.Shared), nil
}

func fileInfoOf(rel string, fi os.FileInfo) model.SharedFile {
	return model.SharedFile{Path: rel, Name: fi.Name(), Size: fi.Size(), IsDir: fi.IsDir(), ModTime: fi.ModTime().UTC()}
}

// ListShared lists one directory (non-recursive) of the shared folder.
func (p *Provider) ListShared(_ context.Context, project, machineID, dir string) ([]model.SharedFile, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return nil, err
	}
	root, err := p.sharedRoot(mi)
	if err != nil {
		return nil, err
	}
	return listDir(root, strings.Trim(dir, "/"), false)
}

func listDir(root, rel string, allowReserved bool) ([]model.SharedFile, error) {
	full, err := resolveIn(root, rel, allowReserved)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, provider.ErrNotFound
		}
		return nil, errf(provider.ErrInvalid, "not a directory")
	}
	out := []model.SharedFile{}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || (rel == "" && e.Name() == reservedDir && !allowReserved) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileInfoOf(path.Join(rel, e.Name()), fi))
		if len(out) >= maxListEntries {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// PutShared stores r at rel in the shared folder, creating parent
// directories. It reads at most maxBytes (provider.ErrTooLarge beyond) and
// writes through a temp file so readers never see a partial upload.
func (p *Provider) PutShared(_ context.Context, project, machineID, rel string, r io.Reader, maxBytes int64, overwrite bool) (*model.SharedFile, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return nil, err
	}
	root, err := p.sharedRoot(mi)
	if err != nil {
		return nil, err
	}
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil, errf(provider.ErrInvalid, "file path is required")
	}
	return putFile(root, rel, r, maxBytes, overwrite, false)
}

func putFile(root, rel string, r io.Reader, maxBytes int64, overwrite, allowReserved bool) (*model.SharedFile, error) {
	full, err := resolveIn(root, rel, allowReserved)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(full); err == nil {
		if fi.IsDir() {
			return nil, errf(provider.ErrInvalid, "%s is a directory", rel)
		}
		if !overwrite {
			return nil, errf(provider.ErrConflict, "%s already exists (use overwrite=true)", rel)
		}
	}
	parent := filepath.Dir(full)
	if err := os.MkdirAll(parent, 0o755); err != nil { // #nosec G301 -- shared with the guest
		return nil, err
	}
	// Re-validate after creating parents (cheap guard against a racing link).
	if _, err := resolveIn(root, path.Dir(rel), allowReserved); err != nil && path.Dir(rel) != "." {
		return nil, err
	}
	tmp, err := os.CreateTemp(parent, ".kryton-upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	n, err := io.Copy(tmp, io.LimitReader(r, maxBytes+1))
	if err != nil {
		cleanup()
		return nil, err
	}
	if n > maxBytes {
		cleanup()
		return nil, errf(provider.ErrTooLarge, "upload exceeds %d bytes", maxBytes)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	sf := fileInfoOf(rel, fi)
	return &sf, nil
}

// OpenShared opens a regular file of the shared folder for download.
func (p *Provider) OpenShared(_ context.Context, project, machineID, rel string) (*os.File, model.SharedFile, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return nil, model.SharedFile{}, err
	}
	root, err := p.sharedRoot(mi)
	if err != nil {
		return nil, model.SharedFile{}, err
	}
	return openFile(root, strings.Trim(rel, "/"), false)
}

func openFile(root, rel string, allowReserved bool) (*os.File, model.SharedFile, error) {
	if rel == "" {
		return nil, model.SharedFile{}, errf(provider.ErrInvalid, "file path is required")
	}
	full, err := resolveIn(root, rel, allowReserved)
	if err != nil {
		return nil, model.SharedFile{}, err
	}
	lfi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, model.SharedFile{}, provider.ErrNotFound
		}
		return nil, model.SharedFile{}, err
	}
	if !lfi.Mode().IsRegular() {
		return nil, model.SharedFile{}, errf(provider.ErrInvalid, "%s is not a regular file", rel)
	}
	f, err := os.Open(full) // #nosec G304 -- path resolved and symlink-checked by resolveIn
	if err != nil {
		return nil, model.SharedFile{}, err
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(lfi, fi) {
		_ = f.Close()
		return nil, model.SharedFile{}, errf(provider.ErrInvalid, "file changed while opening")
	}
	return f, fileInfoOf(rel, fi), nil
}

// DeleteShared removes a file or an empty directory.
func (p *Provider) DeleteShared(_ context.Context, project, machineID, rel string) error {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return err
	}
	root, err := p.sharedRoot(mi)
	if err != nil {
		return err
	}
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return errf(provider.ErrInvalid, "path is required")
	}
	full, err := resolveIn(root, rel, false)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			return provider.ErrNotFound
		}
		return errf(provider.ErrInvalid, "cannot delete %s (directories must be empty)", rel)
	}
	return nil
}
