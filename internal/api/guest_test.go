// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/dockur"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/metrics"
)

type guestEnv struct {
	h        http.Handler
	bus      *events.Bus
	dataDir  string
	rdpUp    *atomic.Bool
	viewer   string
	operator string
	other    string // operator of project "other" only
}

func newGuestEnv(t *testing.T, maxUpload int64) *guestEnv {
	t.Helper()
	ge := &guestEnv{viewer: "v-tok", operator: "o-tok", other: "x-tok", rdpUp: &atomic.Bool{}}
	keys := fmt.Sprintf(`{"keys":[
		{"name":"v","sha256":%q,"role":"viewer","projects":["default"]},
		{"name":"o","sha256":%q,"role":"operator","projects":["default"]},
		{"name":"x","sha256":%q,"role":"operator","projects":["other"]}
	]}`, auth.HashToken(ge.viewer), auth.HashToken(ge.operator), auth.HashToken(ge.other))
	keyFile := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(keyFile, []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{Mode: "apikey", APIKeysFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	ge.dataDir = t.TempDir()
	dp, err := dockur.New(dockur.Config{DataDir: ge.dataDir, Catalog: cat, Hooks: dockur.Hooks{
		Run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), " ps ") {
				return []byte("abc\n"), nil
			}
			return nil, nil
		},
		Dial: func(context.Context, string) (net.Conn, error) {
			c, s := net.Pipe()
			go func() {
				defer func() { _ = s.Close() }()
				if ge.rdpUp.Load() {
					buf := make([]byte, 19)
					_, _ = s.Read(buf)
					_, _ = s.Write([]byte{0x03, 0, 0, 0x0b, 6, 0xd0, 0, 0, 0, 0, 0})
				}
			}()
			return c, nil
		},
		KVMCheck: func() error { return nil },
		PortFree: func(string, int) bool { return true },
	}})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ge.bus, err = events.New(100, "", "", "", log)
	if err != nil {
		t.Fatal(err)
	}
	dp.SetEventSink(func(ctx context.Context, typ, subject string, data map[string]any) {
		ge.bus.Publish(ctx, typ, subject, data)
	})
	var web fs.FS = fstest.MapFS{"index.html": {Data: []byte("ok")}}
	ge.h = New(Config{
		Provider: dp, Catalog: cat, Events: ge.bus, Auth: a, Metrics: metrics.New(), Web: web,
		Projects: []string{"default", "other"}, DefaultProject: "default", AuthMode: "apikey", Log: log,
		MaxUploadBytes: maxUpload,
	}).Handler()
	return ge
}

func (ge *guestEnv) do(t *testing.T, token, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	ge.h.ServeHTTP(w, req)
	return w
}

func (ge *guestEnv) create(t *testing.T, opts string) (id string, body map[string]any) {
	t.Helper()
	w := ge.do(t, ge.operator, "POST", "/api/v1/machines", []byte(fmt.Sprintf(
		`{"name":"win","image":"windows-11-pro","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":64},"dockur":%s}`, opts)))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body["id"].(string), body
}

func TestAPICreateReturnsGeneratedPasswordOnce(t *testing.T) {
	ge := newGuestEnv(t, 0)
	id, body := ge.create(t, `{"jobAgent":true}`)
	pw, _ := body["generatedPassword"].(string)
	if len(pw) < 16 {
		t.Fatalf("no generated password in %v", body)
	}
	w := ge.do(t, ge.viewer, "GET", "/api/v1/machines/"+id, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), pw) || strings.Contains(w.Body.String(), "generatedPassword") {
		t.Fatalf("password echoed on GET: %s", w.Body.String())
	}
}

func TestAPIRejectsHostPathsOnCreate(t *testing.T) {
	ge := newGuestEnv(t, 0)
	w := ge.do(t, ge.operator, "POST", "/api/v1/machines", []byte(
		`{"name":"evil","image":"windows-11-pro","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":64},"dockur":{"sharedDir":"/etc"}}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIPortConflictIs409(t *testing.T) {
	ge := newGuestEnv(t, 0)
	ge.create(t, `{"rdpPort":23389}`)
	w := ge.do(t, ge.operator, "POST", "/api/v1/machines", []byte(
		`{"name":"win2","image":"windows-11-pro","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":64},"dockur":{"rdpPort":23389}}`))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already allocated") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIReadyProbe(t *testing.T) {
	ge := newGuestEnv(t, 0)
	id, _ := ge.create(t, `{}`)
	w := ge.do(t, ge.viewer, "GET", "/api/v1/machines/"+id+"/ready", nil)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"ready":false`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	ge.rdpUp.Store(true)
	w = ge.do(t, ge.viewer, "GET", "/api/v1/machines/"+id+"/ready", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w := ge.do(t, ge.other, "GET", "/api/v1/machines/"+id+"/ready?project=default", nil); w.Code != http.StatusForbidden {
		t.Fatalf("cross-project status=%d", w.Code)
	}
	if w := ge.do(t, ge.other, "GET", "/api/v1/machines/"+id+"/ready?project=other", nil); w.Code != http.StatusNotFound {
		t.Fatalf("other project must not see machine, status=%d", w.Code)
	}
}

func TestAPIRestartWaitAndEvents(t *testing.T) {
	ge := newGuestEnv(t, 0)
	id, _ := ge.create(t, `{}`)
	ge.rdpUp.Store(true)
	w := ge.do(t, ge.viewer, "POST", "/api/v1/machines/"+id+"/restart", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer restart status=%d", w.Code)
	}
	w = ge.do(t, ge.operator, "POST", "/api/v1/machines/"+id+"/restart", []byte(`{"timeoutSeconds":10}`))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatalf("restart status=%d body=%s", w.Code, w.Body.String())
	}
	types := map[string]bool{}
	for _, e := range ge.bus.List(50) {
		types[e.Type] = true
	}
	if !types["io.kryton.machine.restarted"] || !types["io.kryton.machine.ready"] {
		t.Fatalf("events: %v", types)
	}
	ge.rdpUp.Store(false)
	w = ge.do(t, ge.operator, "POST", "/api/v1/machines/"+id+"/restart", []byte(`{"wait":false}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("no-wait restart status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPISharedFolder(t *testing.T) {
	ge := newGuestEnv(t, 16)
	id, _ := ge.create(t, `{"shared":true}`)
	base := "/api/v1/machines/" + id + "/shared"

	if w := ge.do(t, ge.viewer, "PUT", base+"/a/b.txt", []byte("hi")); w.Code != http.StatusForbidden {
		t.Fatalf("viewer upload status=%d", w.Code)
	}
	if w := ge.do(t, ge.other, "PUT", base+"/a/b.txt?project=other", []byte("hi")); w.Code != http.StatusNotFound {
		t.Fatalf("other-project upload status=%d", w.Code)
	}
	w := ge.do(t, ge.operator, "PUT", base+"/a/b.txt", []byte("hello"))
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"path":"a/b.txt"`) {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}
	if w := ge.do(t, ge.operator, "PUT", base+"/a/b.txt", []byte("again")); w.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d", w.Code)
	}
	if w := ge.do(t, ge.operator, "PUT", base+"/a/b.txt?overwrite=true", []byte("again")); w.Code != http.StatusCreated {
		t.Fatalf("overwrite status=%d", w.Code)
	}
	if w := ge.do(t, ge.operator, "PUT", base+"/big.bin", bytes.Repeat([]byte("x"), 17)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status=%d body=%s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"/%2e%2e/%2e%2e/etc/passwd", "/.kryton/jobs/x", "/a/..%2f..%2fx", "/c%3A/x"} {
		if w := ge.do(t, ge.operator, "PUT", base+bad, []byte("x")); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
			t.Fatalf("traversal %q status=%d body=%s", bad, w.Code, w.Body.String())
		}
	}
	w = ge.do(t, ge.viewer, "GET", base+"/a/b.txt", nil)
	if w.Code != 200 || w.Body.String() != "again" || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download status=%d body=%q hdr=%v", w.Code, w.Body.String(), w.Header())
	}
	w = ge.do(t, ge.viewer, "GET", base+"?path=a", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"a/b.txt"`) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	if w := ge.do(t, ge.viewer, "GET", base+"/.kryton/jobs", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("reserved read status=%d", w.Code)
	}
	if w := ge.do(t, ge.operator, "DELETE", base+"/a/b.txt", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", w.Code)
	}
	// No file is written outside the machine directory.
	matches, _ := filepath.Glob(filepath.Join(ge.dataDir, "..", "*.bin"))
	if len(matches) != 0 {
		t.Fatalf("escaped files: %v", matches)
	}
	// Machine without a shared folder.
	if w := ge.do(t, ge.operator, "POST", "/api/v1/machines", []byte(
		`{"name":"plain","image":"windows-11-pro","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":64}}`)); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
}

func TestAPIGuestJobFlow(t *testing.T) {
	ge := newGuestEnv(t, 0)
	id, _ := ge.create(t, `{"jobAgent":true}`)
	jobs := "/api/v1/machines/" + id + "/jobs"

	if w := ge.do(t, ge.viewer, "POST", jobs, []byte(`{"script":"echo hi"}`)); w.Code != http.StatusForbidden {
		t.Fatalf("viewer submit status=%d", w.Code)
	}
	if w := ge.do(t, ge.operator, "POST", jobs, []byte(`{"script":"echo hi","shell":"bash"}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("bad shell status=%d", w.Code)
	}
	w := ge.do(t, ge.operator, "POST", jobs, []byte(`{"script":"echo hi","timeoutSeconds":30}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}
	var job struct {
		ID, State string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &job)
	if job.State != "queued" || job.ID == "" {
		t.Fatalf("job: %s", w.Body.String())
	}
	// Play the guest agent.
	dir := filepath.Join(ge.dataDir, "default")
	entries, _ := os.ReadDir(dir)
	jdir := filepath.Join(dir, entries[0].Name(), "shared", ".kryton", "jobs", job.ID)
	_ = os.WriteFile(filepath.Join(jdir, "stdout.log"), []byte("hi\r\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(jdir, "artifacts"), 0o755)
	_ = os.WriteFile(filepath.Join(jdir, "artifacts", "out.zip"), []byte("PK"), 0o644)
	_ = os.WriteFile(filepath.Join(jdir, "status.json"), []byte(`{"state":"done","exitCode":0}`), 0o644)

	w = ge.do(t, ge.viewer, "GET", jobs+"/"+job.ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"succeeded"`) || !strings.Contains(w.Body.String(), `"out.zip"`) {
		t.Fatalf("get status=%d body=%s", w.Code, w.Body.String())
	}
	w = ge.do(t, ge.viewer, "GET", jobs+"/"+job.ID+"/logs", nil)
	if w.Code != 200 || w.Body.String() != "hi\r\n" || w.Header().Get("X-Log-Size") != "4" {
		t.Fatalf("logs status=%d %q %v", w.Code, w.Body.String(), w.Header())
	}
	w = ge.do(t, ge.viewer, "GET", jobs+"/"+job.ID+"/artifacts/out.zip", nil)
	if w.Code != 200 || w.Body.String() != "PK" {
		t.Fatalf("artifact status=%d %q", w.Code, w.Body.String())
	}
	if w := ge.do(t, ge.viewer, "GET", jobs+"/"+job.ID+"/artifacts/..%2fjob.json", nil); w.Code == 200 {
		t.Fatal("artifact traversal served job.json")
	}
	if w := ge.do(t, ge.viewer, "GET", jobs+"/not-a-job", nil); w.Code != http.StatusNotFound {
		t.Fatalf("bad id status=%d", w.Code)
	}
	w = ge.do(t, ge.viewer, "GET", jobs, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), job.ID) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	n := 0
	for _, e := range ge.bus.List(50) {
		if e.Type == "io.kryton.machine.job.completed" {
			n++
			if e.Data["project"] != "default" || e.Data["jobId"] != job.ID || e.Data["state"] != "succeeded" {
				t.Fatalf("event data: %v", e.Data)
			}
		}
	}
	if n != 1 {
		t.Fatalf("completed events=%d", n)
	}

	// Machines created without the agent cannot take jobs.
	id2, _ := func() (string, map[string]any) {
		w := ge.do(t, ge.operator, "POST", "/api/v1/machines", []byte(
			`{"name":"noagent","image":"windows-11-pro","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":64}}`))
		var b map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		return b["id"].(string), b
	}()
	if w := ge.do(t, ge.operator, "POST", "/api/v1/machines/"+id2+"/jobs", []byte(`{"script":"x"}`)); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "PRECONDITION_FAILED") {
		t.Fatalf("noagent status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIGuestEndpointsUnsupportedOnDemo(t *testing.T) {
	h, _ := testServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/machines/x/ready", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
