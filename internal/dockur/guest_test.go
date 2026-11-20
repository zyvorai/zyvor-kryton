// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package dockur

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// fakeDocker stands in for the container runtime and the guest's RDP port.
type fakeDocker struct {
	mu      sync.Mutex
	calls   [][]string
	logs    string
	running atomic.Bool // `compose ps` reports a running container
	rdpUp   atomic.Bool // the guest answers X.224
	upErr   error
	busy    map[int]bool // host ports reported in use
}

func (f *fakeDocker) run(ctx context.Context, _ string, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil { // like exec.CommandContext: a cancelled context never runs the command
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, " up "):
		return nil, f.upErr
	case strings.Contains(joined, " ps "):
		if f.running.Load() {
			return []byte("abc123\n"), nil
		}
		return nil, nil
	case strings.Contains(joined, " logs "):
		return []byte(f.logs), nil
	}
	return nil, nil
}

func (f *fakeDocker) dial(_ context.Context, _ string) (net.Conn, error) {
	c, s := net.Pipe()
	go func() {
		defer func() { _ = s.Close() }()
		if !f.rdpUp.Load() {
			return // accepts then closes: docker-proxy with no guest behind it
		}
		buf := make([]byte, 19)
		_, _ = s.Read(buf)
		_, _ = s.Write([]byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xd0, 0, 0, 0, 0, 0})
	}()
	return c, nil
}

func (f *fakeDocker) hasCall(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), sub) {
			return true
		}
	}
	return false
}

func newTestProvider(t *testing.T) (*Provider, *fakeDocker) {
	t.Helper()
	f := &fakeDocker{busy: map[int]bool{}}
	f.running.Store(true)
	p, err := New(Config{
		DataDir: t.TempDir(), PublicHost: "lab.example",
		Hooks: Hooks{
			Run: f.run, Dial: f.dial, KVMCheck: func() error { return nil },
			PortFree:  func(_ string, port int) bool { return !f.busy[port] },
			PollEvery: 5 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, f
}

func testSpec(name string, d *model.DockurOptions) model.MachineSpec {
	return model.MachineSpec{Name: name, Image: "windows-11-pro", Compute: model.ComputeSpec{CPU: 4, MemoryMiB: 8192}, Disk: model.DiskSpec{SizeGiB: 64}, Dockur: d}
}

func mustCreate(t *testing.T, p *Provider, name string, d *model.DockurOptions) *model.Machine {
	t.Helper()
	m, err := p.Create(context.Background(), "default", testSpec(name, d))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func readCompose(t *testing.T, p *Provider, m *model.Machine) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.machineDir(m.Project, m.ID), "compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateGeneratesPasswordOnce(t *testing.T) {
	p, _ := newTestProvider(t)
	m := mustCreate(t, p, "w1", nil)
	if len(m.GeneratedPassword) < 16 {
		t.Fatalf("generated password %q too short", m.GeneratedPassword)
	}
	if m.Spec.Dockur.Password != "" {
		t.Fatal("password leaked in spec")
	}
	if !strings.Contains(readCompose(t, p, m), `PASSWORD: "`+m.GeneratedPassword+`"`) {
		t.Fatal("generated password not in compose")
	}
	got, err := p.Get(context.Background(), "default", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GeneratedPassword != "" || got.Spec.Dockur.Password != "" {
		t.Fatal("password returned again by Get")
	}
	// Supplied passwords are kept and never echoed as generated.
	m2 := mustCreate(t, p, "w2", &model.DockurOptions{Password: "Sup3rSecret"})
	if m2.GeneratedPassword != "" || !strings.Contains(readCompose(t, p, m2), `"Sup3rSecret"`) {
		t.Fatal("supplied password mishandled")
	}
}

func TestGeneratePasswordComplexity(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw := generatePassword()
		if len(pw) != 20 || seen[pw] {
			t.Fatalf("bad or repeated password %q", pw)
		}
		seen[pw] = true
		if !strings.ContainsAny(pw, "abcdefghijkmnopqrstuvwxyz") || !strings.ContainsAny(pw, "ABCDEFGHJKLMNPQRSTUVWXYZ") || !strings.ContainsAny(pw, "23456789") {
			t.Fatalf("password %q lacks a character class", pw)
		}
	}
}

func TestBindDefaultsToLoopbackAndPublicIsOptIn(t *testing.T) {
	p, _ := newTestProvider(t)
	m := mustCreate(t, p, "loop", nil)
	c := readCompose(t, p, m)
	if !strings.Contains(c, `"127.0.0.1:18006:8006"`) || !strings.Contains(c, `"127.0.0.1:13389:3389/tcp"`) || strings.Contains(c, "0.0.0.0") {
		t.Fatalf("expected loopback bind:\n%s", c)
	}
	if m.RdpHost != "127.0.0.1" {
		t.Fatalf("rdpHost = %q, want 127.0.0.1", m.RdpHost)
	}
	pub := mustCreate(t, p, "pub", &model.DockurOptions{PublicBind: true})
	c = readCompose(t, p, pub)
	if !strings.Contains(c, `"0.0.0.0:`) {
		t.Fatalf("expected public bind:\n%s", c)
	}
	if pub.RdpHost != "lab.example" {
		t.Fatalf("rdpHost = %q, want lab.example", pub.RdpHost)
	}
}

func TestPortAllocationAndConflicts(t *testing.T) {
	p, f := newTestProvider(t)
	a := mustCreate(t, p, "a", nil)
	b := mustCreate(t, p, "b", nil)
	if a.RdpPort == b.RdpPort || a.RdpPort != 13389 || b.RdpPort != 13390 {
		t.Fatalf("auto ports a=%d b=%d", a.RdpPort, b.RdpPort)
	}
	// Explicit port already taken by another machine.
	_, err := p.Create(context.Background(), "default", testSpec("c", &model.DockurOptions{RdpPort: a.RdpPort}))
	if !errors.Is(err, provider.ErrConflict) || !strings.Contains(err.Error(), "already allocated") {
		t.Fatalf("want conflict, got %v", err)
	}
	// Explicit port busy on the host.
	f.busy[14000] = true
	_, err = p.Create(context.Background(), "default", testSpec("d", &model.DockurOptions{RdpPort: 14000}))
	if !errors.Is(err, provider.ErrConflict) || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("want in-use conflict, got %v", err)
	}
	// Explicit free ports are honoured, and auto ports skip host-busy ones.
	e, err := p.Create(context.Background(), "default", testSpec("e", &model.DockurOptions{RdpPort: 14001, ConsolePort: 14002}))
	if err != nil || e.RdpPort != 14001 {
		t.Fatalf("explicit ports: %v %+v", err, e)
	}
	f.busy[13391] = true
	g := mustCreate(t, p, "g", nil)
	if g.RdpPort != 13392 {
		t.Fatalf("auto port should skip busy 13391, got %d", g.RdpPort)
	}
	// A failed create must not leak a reservation.
	for _, pp := range p.ports {
		if pp.RDP == 14000 {
			t.Fatal("conflicting create leaked a port reservation")
		}
	}
}

func TestARMUnsupported(t *testing.T) {
	p, _ := newTestProvider(t)
	spec := testSpec("arm", nil)
	spec.Image = "windows-11-arm64"
	if _, err := p.Create(context.Background(), "default", spec); !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("want unsupported, got %v", err)
	}
}

func TestHostPathOptionsRejected(t *testing.T) {
	p, _ := newTestProvider(t)
	for _, d := range []*model.DockurOptions{
		{SharedDir: "/etc"}, {OemDir: "../../etc"}, {SharedDir: "storage"}, {SharedDir: "x", OemDir: "x"},
	} {
		if _, err := p.Create(context.Background(), "default", testSpec("bad", d)); !errors.Is(err, provider.ErrInvalid) {
			t.Fatalf("%+v: want ErrInvalid, got %v", d, err)
		}
	}
	if err := model.ValidateMachineSpec(testSpec("ok", &model.DockurOptions{SharedDir: "/etc"})); err == nil {
		t.Fatal("model validation must reject absolute sharedDir")
	}
}

func TestSharedAndOemMounts(t *testing.T) {
	p, _ := newTestProvider(t)
	m := mustCreate(t, p, "oem", &model.DockurOptions{Shared: true, OemScript: "echo hello\necho world", Username: "bob"})
	c := readCompose(t, p, m)
	if !strings.Contains(c, `"./shared:/shared"`) || !strings.Contains(c, `"./oem:/oem"`) {
		t.Fatalf("mounts missing:\n%s", c)
	}
	dir := p.machineDir(m.Project, m.ID)
	bat, err := os.ReadFile(filepath.Join(dir, "oem", "install.bat"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(bat, []byte("echo hello\r\necho world")) || bytes.Contains(bat, []byte("kryton-agent")) {
		t.Fatalf("install.bat wrong: %q", bat)
	}
	if fi, err := os.Stat(filepath.Join(dir, "shared")); err != nil || !fi.IsDir() {
		t.Fatal("shared dir not created under the machine dir")
	}
	agent := mustCreate(t, p, "agent", &model.DockurOptions{JobAgent: true})
	adir := p.machineDir(agent.Project, agent.ID)
	bat, _ = os.ReadFile(filepath.Join(adir, "oem", "install.bat"))
	if !bytes.Contains(bat, []byte("kryton-agent.ps1")) {
		t.Fatalf("agent bootstrap missing: %q", bat)
	}
	if _, err := os.Stat(filepath.Join(adir, "oem", "kryton-agent.ps1")); err != nil {
		t.Fatal("agent script not written")
	}
	if !readSpecShared(t, p, agent) {
		t.Fatal("jobAgent should imply shared")
	}
}

func readSpecShared(t *testing.T, p *Provider, m *model.Machine) bool {
	t.Helper()
	got, err := p.Get(context.Background(), m.Project, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Spec.Dockur.Shared
}

func TestResolveInRejectsTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	for _, rel := range []string{"../x", "a/../../x", "/abs", "a//b", ".", "a\\b", "c:/x", "link/secret", ".kryton/jobs", "a/\x00"} {
		if _, err := resolveIn(root, rel, false); !errors.Is(err, provider.ErrInvalid) {
			t.Errorf("resolveIn(%q): want ErrInvalid, got %v", rel, err)
		}
	}
	if p, err := resolveIn(root, "ok/sub/file.txt", false); err != nil || !strings.HasPrefix(p, root) {
		t.Fatalf("valid path rejected: %v %s", err, p)
	}
	if _, err := resolveIn(root, ".kryton/jobs", true); err != nil {
		t.Fatalf("reserved allowed internally: %v", err)
	}
}

func TestSharedFileOperations(t *testing.T) {
	p, _ := newTestProvider(t)
	ctx := context.Background()
	m := mustCreate(t, p, "files", &model.DockurOptions{Shared: true})

	sf, err := p.PutShared(ctx, m.Project, m.ID, "build/out.txt", strings.NewReader("hello"), 100, false)
	if err != nil || sf.Size != 5 || sf.Path != "build/out.txt" {
		t.Fatalf("put: %v %+v", err, sf)
	}
	if _, err := p.PutShared(ctx, m.Project, m.ID, "build/out.txt", strings.NewReader("x"), 100, false); !errors.Is(err, provider.ErrConflict) {
		t.Fatalf("want conflict without overwrite, got %v", err)
	}
	if _, err := p.PutShared(ctx, m.Project, m.ID, "build/out.txt", strings.NewReader("replaced"), 100, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PutShared(ctx, m.Project, m.ID, "big.bin", strings.NewReader(strings.Repeat("a", 101)), 100, false); !errors.Is(err, provider.ErrTooLarge) {
		t.Fatalf("want too large, got %v", err)
	}
	dir := filepath.Join(p.machineDir(m.Project, m.ID), "shared")
	if _, err := os.Stat(filepath.Join(dir, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("oversize upload left a file behind")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*", ".kryton-upload-*")); len(matches) != 0 {
		t.Fatalf("temp files left: %v", matches)
	}
	for _, bad := range []string{"../escape", ".kryton/jobs/x", "a/../../b"} {
		if _, err := p.PutShared(ctx, m.Project, m.ID, bad, strings.NewReader("x"), 100, true); !errors.Is(err, provider.ErrInvalid) {
			t.Fatalf("put %q: want ErrInvalid, got %v", bad, err)
		}
	}
	list, err := p.ListShared(ctx, m.Project, m.ID, "")
	if err != nil || len(list) != 1 || list[0].Path != "build" || !list[0].IsDir {
		t.Fatalf("root listing (hides .kryton): %v %+v", err, list)
	}
	list, _ = p.ListShared(ctx, m.Project, m.ID, "build")
	if len(list) != 1 || list[0].Path != "build/out.txt" {
		t.Fatalf("listing: %+v", list)
	}
	f, info, err := p.OpenShared(ctx, m.Project, m.ID, "build/out.txt")
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 20)
	n, _ := f.Read(b)
	_ = f.Close()
	if string(b[:n]) != "replaced" || info.Size != 8 {
		t.Fatalf("download got %q %+v", b[:n], info)
	}
	// A symlink planted by the guest must not be followed.
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "planted")); err == nil {
		if _, _, err := p.OpenShared(ctx, m.Project, m.ID, "planted"); !errors.Is(err, provider.ErrInvalid) {
			t.Fatalf("symlink followed: %v", err)
		}
	}
	if err := p.DeleteShared(ctx, m.Project, m.ID, "build/out.txt"); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteShared(ctx, m.Project, m.ID, "build/out.txt"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	plain := mustCreate(t, p, "noshare", nil)
	if _, err := p.ListShared(ctx, plain.Project, plain.ID, ""); !errors.Is(err, provider.ErrPrecondition) {
		t.Fatalf("want precondition, got %v", err)
	}
	if _, err := p.ListShared(ctx, "other", m.ID, ""); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("project scope not enforced: %v", err)
	}
}

type recordedEvent struct {
	typ  string
	data map[string]any
}

func jobProvider(t *testing.T) (*Provider, *fakeDocker, *model.Machine, *[]recordedEvent, string) {
	t.Helper()
	p, f := newTestProvider(t)
	evs := &[]recordedEvent{}
	p.SetEventSink(func(_ context.Context, typ, _ string, data map[string]any) {
		evMu.Lock()
		*evs = append(*evs, recordedEvent{typ, data})
		evMu.Unlock()
	})
	m := mustCreate(t, p, "jobs", &model.DockurOptions{JobAgent: true})
	return p, f, m, evs, filepath.Join(p.machineDir(m.Project, m.ID), "shared", ".kryton", "jobs")
}

func agentWrite(t *testing.T, jobsDir, jobID, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(jobsDir, jobID, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var evMu sync.Mutex

func countType(evs *[]recordedEvent, typ string) int {
	evMu.Lock()
	defer evMu.Unlock()
	n := 0
	for _, e := range *evs {
		if e.typ == typ {
			n++
		}
	}
	return n
}

func TestJobLifecycleAndCompletionEvent(t *testing.T) {
	p, _, m, evs, jobsDir := jobProvider(t)
	ctx := context.Background()
	job, err := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Name: "build", Script: "echo hi\nexit /b 3", Env: map[string]string{"CONFIG": "Release"}})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.GuestJobQueued || job.Shell != "cmd" || job.TimeoutSeconds != 600 {
		t.Fatalf("defaults: %+v", job)
	}
	script, _ := os.ReadFile(filepath.Join(jobsDir, job.ID, "script.cmd"))
	if !strings.Contains(string(script), "echo hi\r\nexit /b 3") {
		t.Fatalf("script: %q", script)
	}
	rec, _ := os.ReadFile(filepath.Join(jobsDir, job.ID, "job.json"))
	if !bytes.Contains(rec, []byte(`"scriptFile":"script.cmd"`)) || !bytes.Contains(rec, []byte(`"CONFIG":"Release"`)) {
		t.Fatalf("job.json: %s", rec)
	}
	if got, _ := p.GetJob(ctx, m.Project, m.ID, job.ID); got.State != model.GuestJobQueued {
		t.Fatalf("want queued, got %s", got.State)
	}

	// The agent picks the job up.
	agentWrite(t, jobsDir, job.ID, "status.json", `{"state":"running","startedAt":"`+time.Now().UTC().Format(time.RFC3339)+`"}`)
	agentWrite(t, jobsDir, job.ID, "stdout.log", "line1\r\nline2\r\n")
	if got, _ := p.GetJob(ctx, m.Project, m.ID, job.ID); got.State != model.GuestJobRunning || got.StartedAt == nil {
		t.Fatalf("want running, got %+v", got)
	}
	if countType(evs, "io.kryton.machine.job.completed") != 0 {
		t.Fatal("completed event fired early")
	}

	// It finishes with a UTF-8 BOM (PowerShell 5 style) and one artifact.
	agentWrite(t, jobsDir, job.ID, "status.json", "\xef\xbb\xbf"+`{"state":"done","exitCode":3,"startedAt":"2026-01-01T00:00:00Z","finishedAt":"2026-01-01T00:00:05Z"}`)
	if err := os.MkdirAll(filepath.Join(jobsDir, job.ID, "artifacts", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentWrite(t, jobsDir, job.ID, "artifacts/bin/app.exe", "MZ")
	got, err := p.GetJob(ctx, m.Project, m.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.GuestJobFailed || got.ExitCode == nil || *got.ExitCode != 3 || got.FinishedAt == nil {
		t.Fatalf("want failed exit 3, got %+v", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Path != "bin/app.exe" {
		t.Fatalf("artifacts: %+v", got.Artifacts)
	}
	// Polling again (and the monitor) must not re-publish.
	_, _ = p.GetJob(ctx, m.Project, m.ID, job.ID)
	p.scanJobs(ctx)
	if n := countType(evs, "io.kryton.machine.job.completed"); n != 1 {
		t.Fatalf("completed events = %d, want 1", n)
	}
	if (*evs)[len(*evs)-1].data["exitCode"] != 3 || (*evs)[len(*evs)-1].data["state"] != model.GuestJobFailed {
		t.Fatalf("event data: %+v", (*evs)[len(*evs)-1].data)
	}

	log, total, err := p.JobLog(ctx, m.Project, m.ID, job.ID, 0, 5)
	if err != nil || string(log) != "line1" || total != 14 {
		t.Fatalf("log: %q %d %v", log, total, err)
	}
	f, sf, err := p.OpenArtifact(ctx, m.Project, m.ID, job.ID, "bin/app.exe")
	if err != nil || sf.Size != 2 {
		t.Fatalf("artifact: %v %+v", err, sf)
	}
	_ = f.Close()
	if _, _, err := p.OpenArtifact(ctx, m.Project, m.ID, job.ID, "/etc/passwd"); err == nil {
		t.Fatal("absolute artifact path must not open a host file")
	}
	for _, bad := range []string{"../job.json", "../../x"} {
		if _, _, err := p.OpenArtifact(ctx, m.Project, m.ID, job.ID, bad); !errors.Is(err, provider.ErrInvalid) {
			t.Fatalf("artifact %q: want ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := p.GetJob(ctx, m.Project, m.ID, "../../etc"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("bad job id: %v", err)
	}
}

func TestJobSuccessViaMonitor(t *testing.T) {
	p, _, m, evs, jobsDir := jobProvider(t)
	job, _ := p.SubmitJob(context.Background(), m.Project, m.ID, model.GuestJobSpec{Script: "echo ok", Shell: "powershell"})
	agentWrite(t, jobsDir, job.ID, "status.json", `{"state":"done","exitCode":0}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go p.RunMonitor(ctx, 10*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if countType(evs, "io.kryton.machine.job.completed") == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if countType(evs, "io.kryton.machine.job.completed") != 1 {
		t.Fatal("monitor did not publish completion")
	}
	got, _ := p.GetJob(context.Background(), m.Project, m.ID, job.ID)
	if got.State != model.GuestJobSucceeded {
		t.Fatalf("state %s", got.State)
	}
	if _, err := os.Stat(filepath.Join(jobsDir, job.ID, "script.ps1")); err != nil {
		t.Fatal("powershell script not written")
	}
}

func TestJobTimeoutAndQueueExpiry(t *testing.T) {
	p, _, m, evs, jobsDir := jobProvider(t)
	ctx := context.Background()
	// Running past timeout + overrun with a silent agent -> timed_out.
	job, _ := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Script: "sleep", TimeoutSeconds: 5})
	old := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	agentWrite(t, jobsDir, job.ID, "status.json", `{"state":"running","startedAt":"`+old+`"}`)
	got, _ := p.GetJob(ctx, m.Project, m.ID, job.ID)
	if got.State != model.GuestJobTimedOut {
		t.Fatalf("want timed_out, got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(jobsDir, job.ID, "cancel")); err != nil {
		t.Fatal("cancel marker not written so the agent kills the process")
	}
	// Agent-reported timeout.
	j2, _ := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Script: "sleep", TimeoutSeconds: 5})
	agentWrite(t, jobsDir, j2.ID, "status.json", `{"state":"timed_out"}`)
	if got, _ := p.GetJob(ctx, m.Project, m.ID, j2.ID); got.State != model.GuestJobTimedOut {
		t.Fatalf("agent timeout: %+v", got)
	}
	// Never picked up.
	p.queueTimeout = time.Millisecond
	j3, _ := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Script: "x"})
	time.Sleep(5 * time.Millisecond)
	if got, _ := p.GetJob(ctx, m.Project, m.ID, j3.ID); got.State != model.GuestJobFailed || !strings.Contains(got.Message, "no guest agent") {
		t.Fatalf("queue expiry: %+v", got)
	}
	if n := countType(evs, "io.kryton.machine.job.completed"); n != 3 {
		t.Fatalf("completed events = %d, want 3", n)
	}
}

func TestJobCancel(t *testing.T) {
	p, _, m, evs, jobsDir := jobProvider(t)
	ctx := context.Background()
	q, _ := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Script: "echo"})
	got, err := p.CancelJob(ctx, m.Project, m.ID, q.ID)
	if err != nil || got.State != model.GuestJobCanceled {
		t.Fatalf("queued cancel: %v %+v", err, got)
	}
	if b, _ := os.ReadFile(filepath.Join(jobsDir, q.ID, "status.json")); !strings.Contains(string(b), "canceled") {
		t.Fatal("agent must see a canceled status so it never starts the job")
	}
	r, _ := p.SubmitJob(ctx, m.Project, m.ID, model.GuestJobSpec{Script: "echo"})
	agentWrite(t, jobsDir, r.ID, "status.json", `{"state":"running"}`)
	got, _ = p.CancelJob(ctx, m.Project, m.ID, r.ID)
	if got.State != model.GuestJobRunning || !got.CancelRequest {
		t.Fatalf("running cancel should wait for agent: %+v", got)
	}
	agentWrite(t, jobsDir, r.ID, "status.json", `{"state":"canceled"}`)
	if got, _ = p.GetJob(ctx, m.Project, m.ID, r.ID); got.State != model.GuestJobCanceled {
		t.Fatalf("after agent: %+v", got)
	}
	if n := countType(evs, "io.kryton.machine.job.completed"); n != 2 {
		t.Fatalf("completed events = %d, want 2", n)
	}
}

func TestJobValidationAndPreconditions(t *testing.T) {
	p, _, m, _, _ := jobProvider(t)
	ctx := context.Background()
	for _, spec := range []model.GuestJobSpec{
		{Script: ""}, {Script: "x", Shell: "bash"}, {Script: "x", TimeoutSeconds: 999999},
		{Script: "x", Env: map[string]string{"bad name": "1"}}, {Script: "x", Env: map[string]string{"KRYTON_X": "1"}},
		{Script: strings.Repeat("a", model.MaxScriptBytes+1)},
	} {
		if _, err := p.SubmitJob(ctx, m.Project, m.ID, spec); !errors.Is(err, provider.ErrInvalid) {
			t.Fatalf("%+v: want ErrInvalid, got %v", spec.Name, err)
		}
	}
	noAgent := mustCreate(t, p, "plain", &model.DockurOptions{Shared: true})
	if _, err := p.SubmitJob(ctx, noAgent.Project, noAgent.ID, model.GuestJobSpec{Script: "x"}); !errors.Is(err, provider.ErrPrecondition) {
		t.Fatalf("want precondition, got %v", err)
	}
	if _, err := p.SubmitJob(ctx, "other", m.ID, model.GuestJobSpec{Script: "x"}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("project scope: %v", err)
	}
}

func TestReadinessProbe(t *testing.T) {
	p, f, m, _, _ := jobProvider(t)
	ctx := context.Background()
	hb := filepath.Join(p.machineDir(m.Project, m.ID), "shared", ".kryton", "agent-ready")

	f.running.Store(false)
	rd, err := p.Ready(ctx, m.Project, m.ID)
	if err != nil || rd.Ready || rd.ContainerUp {
		t.Fatalf("container down: %v %+v", err, rd)
	}

	// Container up and the published port accepts, but no guest behind it.
	f.running.Store(true)
	f.logs = "❯ Downloading Windows 11 Pro... 50%\n"
	rd, _ = p.Ready(ctx, m.Project, m.ID)
	if rd.Ready || !rd.ContainerUp || rd.RDPOpen {
		t.Fatalf("rdp must not count as open on a bare port: %+v", rd)
	}
	if rd.Install == nil || rd.Install.Phase != "downloading" || rd.Install.Percent != 25 {
		t.Fatalf("install: %+v", rd.Install)
	}

	// RDP answers but the agent has not reported in.
	f.rdpUp.Store(true)
	rd, _ = p.Ready(ctx, m.Project, m.ID)
	if rd.Ready || !rd.RDPOpen || !rd.AgentRequired || rd.AgentPresent {
		t.Fatalf("agent missing: %+v", rd)
	}
	if err := os.WriteFile(hb, []byte("now"), 0o644); err != nil {
		t.Fatal(err)
	}
	rd, _ = p.Ready(ctx, m.Project, m.ID)
	if !rd.Ready || rd.Install.Phase != "complete" || rd.Install.Percent != 100 {
		t.Fatalf("want ready: %+v", rd)
	}
	got, _ := p.Get(ctx, m.Project, m.ID)
	if got.State != model.StateRunning {
		t.Fatalf("state = %s, want running", got.State)
	}
	// A stale heartbeat no longer counts.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(hb, old, old)
	if rd, _ = p.Ready(ctx, m.Project, m.ID); rd.Ready || rd.AgentPresent {
		t.Fatalf("stale heartbeat accepted: %+v", rd)
	}
}

func TestRestartGracefulWaitsForRDP(t *testing.T) {
	p, f, _, _, _ := jobProvider(t)
	ctx := context.Background()
	m := mustCreate(t, p, "plain-restart", nil)
	f.rdpUp.Store(false)
	go func() {
		time.Sleep(60 * time.Millisecond)
		f.rdpUp.Store(true)
	}()
	got, rd, err := p.Restart(ctx, m.Project, m.ID, provider.RestartOptions{Wait: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if rd == nil || !rd.Ready || got.State != model.StateRunning {
		t.Fatalf("restart wait: %+v / %s", rd, got.State)
	}
	f.mu.Lock()
	var order []string
	for _, c := range f.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, " stop -t 120") {
			order = append(order, "stop")
		}
		if strings.HasSuffix(j, " start") {
			order = append(order, "start")
		}
	}
	f.mu.Unlock()
	if strings.Join(order, ",") != "stop,start" {
		t.Fatalf("graceful order = %v", order)
	}
	if f.hasCall(" kill") {
		t.Fatal("graceful restart must not kill")
	}
	if _, _, err := p.Restart(ctx, m.Project, m.ID, provider.RestartOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if !f.hasCall(" kill") {
		t.Fatal("force restart should kill")
	}
	// No wait: returns immediately in a starting state.
	f.rdpUp.Store(false)
	got, rd, err = p.Restart(ctx, m.Project, m.ID, provider.RestartOptions{})
	if err != nil || rd != nil || got.State != model.StateStarting {
		t.Fatalf("no-wait restart: %v %v %+v", err, rd, got)
	}
	// Timeout returns the not-ready reading rather than an error.
	_, rd, err = p.Restart(ctx, m.Project, m.ID, provider.RestartOptions{Wait: true, Timeout: 30 * time.Millisecond})
	if err != nil || rd == nil || rd.Ready {
		t.Fatalf("timeout restart: %v %+v", err, rd)
	}
	if _, _, err := p.Restart(ctx, "other", m.ID, provider.RestartOptions{}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("project scope: %v", err)
	}
}

func TestInstallProgressParsing(t *testing.T) {
	cases := []struct {
		logs    string
		phase   string
		percent int
	}{
		{"", "unknown", 1},
		{"❯ Starting Windows for Docker v4.0...\n", "starting", 3},
		{"\x1b[1;34m❯\x1b[0m Downloading Windows 11 Pro...\n", "downloading", 5},
		{"windows-1  | ❯ Downloading Windows 10 Pro: 80%\n", "downloading", 37},
		{"❯ Extracting Windows ISO...\n", "extracting", 50},
		{"❯ Adding XML file for automatic installation...\n", "building", 60},
		{"❯ Building Windows image...\n❯ Windows started successfully, visit http://127.0.0.1:8006/\n", "installing", 70},
		{"❯ Windows started successfully\n❯ Shutdown completed!\n", "stopped", 0},
	}
	for _, c := range cases {
		got := parseInstallProgress(c.logs)
		if got.Phase != c.phase || got.Percent != c.percent {
			t.Errorf("%q: got %+v, want %s/%d", c.logs, got, c.phase, c.percent)
		}
	}
}

func TestProbeRDPSpeaksX224(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback")
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 19)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xd0, 0, 0, 0, 0, 0})
				_ = c.Close()
			}()
		}
	}()
	realDial := func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	if !probeRDP(context.Background(), realDial, ln.Addr().String()) {
		t.Fatal("real RDP-like listener not detected")
	}
	// An HTTP-ish responder is not RDP.
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = ln2.Close() }()
	go func() {
		c, err := ln2.Accept()
		if err == nil {
			_, _ = c.Write([]byte("HTTP/1.1 400"))
			_ = c.Close()
		}
	}()
	if probeRDP(context.Background(), realDial, ln2.Addr().String()) {
		t.Fatal("HTTP responder must not look like RDP")
	}
}

func TestStatePersistsPortBind(t *testing.T) {
	p, _ := newTestProvider(t)
	m := mustCreate(t, p, "persist", &model.DockurOptions{Shared: true})
	p2, err := New(Config{DataDir: p.dataDir, PublicHost: "lab.example", Hooks: Hooks{Run: (&fakeDocker{}).run}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p2.Get(context.Background(), m.Project, m.ID)
	if err != nil || got.RdpHost != "127.0.0.1" || got.RdpPort != m.RdpPort {
		t.Fatalf("reload: %v %+v", err, got)
	}
	var s persisted
	b, _ := os.ReadFile(p.statePath())
	if err := json.Unmarshal(b, &s); err != nil || s.Ports[m.ID].Bind != "127.0.0.1" {
		t.Fatalf("bind not persisted: %v %s", err, b)
	}
}

// A client that disconnects mid-restart must not leave the guest powered off:
// stop and start are issued on a context detached from the request.
func TestRestartSurvivesCancelledRequest(t *testing.T) {
	p, f, _, _, _ := jobProvider(t)
	m := mustCreate(t, p, "cancelled-restart", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := p.Restart(ctx, m.Project, m.ID, provider.RestartOptions{}); err != nil {
		t.Fatalf("restart with cancelled request context: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var started bool
	for _, c := range f.calls {
		if strings.HasSuffix(strings.Join(c, " "), " start") {
			started = true
		}
	}
	if !started {
		t.Fatal("guest was stopped but never started again")
	}
}

// The agent must refresh the guest SMB client's cached view of the jobs
// directory (jobs are created on the host, outside Samba), or new jobs stay
// queued until the next reboot.
func TestAgentRefreshesJobsDirectory(t *testing.T) {
	if !strings.Contains(agentScript, "function Sync-JobsDir") || !strings.Contains(agentScript, "Sync-JobsDir $jobs") {
		t.Fatal("agent.ps1 no longer refreshes the jobs directory before listing it")
	}
	if strings.Index(agentScript, "Sync-JobsDir $jobs") > strings.Index(agentScript, "Get-ChildItem -LiteralPath $jobs") {
		t.Fatal("Sync-JobsDir must run before the jobs listing")
	}
}

// Deleting with an already-cancelled request context must still tear the guest
// down; otherwise the record and data vanish while the container keeps running.
func TestDeleteTearsDownEvenIfRequestCancelled(t *testing.T) {
	p, f, _, _, _ := jobProvider(t)
	m := mustCreate(t, p, "cancelled-delete", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Delete(ctx, m.Project, m.ID); err != nil {
		t.Fatalf("delete with cancelled request context: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var down bool
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), " down ") {
			down = true
		}
	}
	if !down {
		t.Fatal("machine record deleted but `compose down` never ran")
	}
}
