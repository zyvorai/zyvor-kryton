// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package imagebuild acquires checksum-pinned, immutable Linux base images.
package imagebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/zyvorai/kryton/internal/model"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var imageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)

type Artifact struct {
	ImageID    string    `json:"imageId"`
	SourceURL  string    `json:"sourceUrl"`
	SHA256     string    `json:"sha256"`
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	AcquiredAt time.Time `json:"acquiredAt"`
	// Acquired images have not been boot-certified.
	BootValidated bool `json:"bootValidated"`
}
type Store struct {
	Dir      string
	Client   *http.Client
	MaxBytes int64
}

func (s Store) Fetch(ctx context.Context, img model.Image, source, digest string) (Artifact, error) {
	if img.OS != "linux" || !imageRE.MatchString(img.ID) {
		return Artifact{}, fmt.Errorf("valid Linux image required")
	}
	if !digestRE.MatchString(digest) {
		return Artifact{}, fmt.Errorf("approved lowercase SHA-256 digest required")
	}
	if source == "" {
		source = img.SourceURL
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return Artifact{}, fmt.Errorf("source must be an HTTPS URL without credentials")
	}
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return Artifact{}, err
	}
	// #nosec G301 -- public distribution artifacts, no credentials
	if err = os.MkdirAll(dir, 0755); err != nil {
		return Artifact{}, err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return Artifact{}, err
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.User != nil || len(via) >= 10 {
				return fmt.Errorf("unsafe redirect")
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Artifact{}, fmt.Errorf("image HTTP status %d", resp.StatusCode)
	}
	limit := s.MaxBytes
	if limit <= 0 {
		limit = 32 << 30
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Artifact{}, err
	}
	if n > limit {
		return Artifact{}, fmt.Errorf("image exceeds size limit")
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return Artifact{}, fmt.Errorf("SHA-256 mismatch; image not published")
	}
	if err = f.Sync(); err != nil {
		return Artifact{}, err
	}
	if err = f.Close(); err != nil {
		return Artifact{}, err
	}
	path := filepath.Join(dir, digest+".qcow2")
	// Link is atomic and cannot overwrite an existing immutable artifact.
	// #nosec G302 -- immutable public distribution disk
	if err = os.Chmod(f.Name(), 0444); err != nil {
		return Artifact{}, err
	}
	if err = os.Link(f.Name(), path); err != nil {
		if !os.IsExist(err) {
			return Artifact{}, err
		}
		if err = verify(path, digest); err != nil {
			return Artifact{}, err
		}
	}
	art := Artifact{ImageID: img.ID, SourceURL: source, SHA256: digest, Path: path, Bytes: n, AcquiredAt: time.Now().UTC()}
	b, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return Artifact{}, err
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(b); err != nil {
		_ = tmp.Close()
		return Artifact{}, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Artifact{}, err
	}
	if err = tmp.Close(); err != nil {
		return Artifact{}, err
	}
	// #nosec G302 -- public artifact provenance, no credentials
	if err = os.Chmod(tmp.Name(), 0644); err != nil {
		return Artifact{}, err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, img.ID+".json")); err != nil {
		return Artifact{}, err
	}
	return art, nil
}

// Get returns the artifact after re-hashing it against its manifest digest.
func (s Store) Get(imageID string) (Artifact, error) {
	a, err := s.manifest(imageID)
	if err != nil {
		return a, err
	}
	if err = verify(a.Path, a.SHA256); err != nil {
		return a, err
	}
	return a, nil
}

// Lookup validates the manifest and the artifact's type and size without
// hashing, for listings polled by the UI. Provisioning must use Get.
func (s Store) Lookup(imageID string) (Artifact, error) {
	a, err := s.manifest(imageID)
	if err != nil {
		return a, err
	}
	info, err := os.Lstat(a.Path)
	if err != nil {
		return a, err
	}
	if !info.Mode().IsRegular() || info.Size() != a.Bytes {
		return a, fmt.Errorf("artifact does not match manifest")
	}
	return a, nil
}

func (s Store) manifest(imageID string) (Artifact, error) {
	if !imageRE.MatchString(imageID) {
		return Artifact{}, fmt.Errorf("invalid image id")
	}
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return Artifact{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, imageID+".json")) // #nosec G304 -- imageID validated; directory supplied by operator
	if err != nil {
		return Artifact{}, err
	}
	var a Artifact
	if err = json.Unmarshal(b, &a); err != nil {
		return a, err
	}
	if a.ImageID != imageID || !digestRE.MatchString(a.SHA256) || a.Path != filepath.Join(dir, a.SHA256+".qcow2") {
		return a, fmt.Errorf("invalid artifact manifest")
	}
	return a, nil
}
func verify(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact must be a regular file")
	}
	f, err := os.Open(path) // #nosec G304 -- digest path beneath operator store; Lstat rejects symlinks
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("artifact checksum mismatch")
	}
	return nil
}

// CDI returns a persistent import and stable DataSource. URL must point to the
// verified artifact published by the operator, not the moving upstream URL.
func CDI(img model.Image, namespace, sourceURL, storageClass string) ([]map[string]any, error) {
	if err := model.ValidateProject(namespace); err != nil {
		return nil, err
	}
	if img.OS != "linux" || !imageRE.MatchString(img.ID) {
		return nil, fmt.Errorf("linux image required")
	}
	u, err := url.Parse(sourceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("HTTPS artifact URL required")
	}
	storage := map[string]any{"accessModes": []string{"ReadWriteOnce"}, "volumeMode": "Filesystem", "resources": map[string]any{"requests": map[string]any{"storage": fmt.Sprintf("%dGi", img.DefaultDiskGB)}}}
	if storageClass != "" {
		storage["storageClassName"] = storageClass
	}
	return []map[string]any{
		{"apiVersion": "cdi.kubevirt.io/v1beta1", "kind": "DataVolume", "metadata": map[string]any{"name": img.ID, "namespace": namespace}, "spec": map[string]any{"source": map[string]any{"http": map[string]any{"url": sourceURL}}, "storage": storage}},
		{"apiVersion": "cdi.kubevirt.io/v1beta1", "kind": "DataSource", "metadata": map[string]any{"name": img.ID, "namespace": namespace}, "spec": map[string]any{"source": map[string]any{"pvc": map[string]any{"name": img.ID, "namespace": namespace}}}},
	}, nil
}
