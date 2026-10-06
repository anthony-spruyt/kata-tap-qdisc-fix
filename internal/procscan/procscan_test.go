package procscan

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/qdisc"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// buildFakeProcTree links <root>/<pid>/ns/net to one regular file per netns ID,
// so Stat resolves shared IDs to the same inode. Returns netns ID to inode.
func buildFakeProcTree(t *testing.T, root string, pidToNsID map[int]string) (nsIDToInode map[string]uint64) {
	t.Helper()
	nsDir := filepath.Join(root, "_netns_files")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	nsIDToInode = make(map[string]uint64)
	for _, nsID := range pidToNsID {
		if _, ok := nsIDToInode[nsID]; ok {
			continue
		}
		nsFile := filepath.Join(nsDir, nsID)
		if err := os.WriteFile(nsFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		var st syscall.Stat_t
		if err := syscall.Stat(nsFile, &st); err != nil {
			t.Fatal(err)
		}
		nsIDToInode[nsID] = st.Ino
	}

	for pid, nsID := range pidToNsID {
		pidStr := strconv.Itoa(pid)
		nsSubdir := filepath.Join(root, pidStr, "ns")
		if err := os.MkdirAll(nsSubdir, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(nsDir, nsID)
		linkPath := filepath.Join(nsSubdir, "net")
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatal(err)
		}
	}
	return nsIDToInode
}

func TestProcScannerDedup(t *testing.T) {
	root := t.TempDir()

	pidToNsID := map[int]string{
		100: "nsA",
		101: "nsA",
		200: "nsB",
	}
	buildFakeProcTree(t, root, pidToNsID)

	const hostInode = uint64(999999999)

	var sweepCount int
	opener := &countingOpener{fn: func(_ string) { sweepCount++ }}

	scanner := New(
		opener,
		func() qdisc.Manager { return &fakeManager{links: nil} },
		false,
		silentLogger(),
		hostInode,
		root,
	)

	result, err := scanner.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() error: %v", err)
	}

	if sweepCount != 2 {
		t.Errorf("DoInNetns called %d times, want 2 (one per unique netns)", sweepCount)
	}
	if result.UniqueNetns != 2 {
		t.Errorf("UniqueNetns = %d, want 2", result.UniqueNetns)
	}
	if result.TotalInodes != 3 {
		t.Errorf("TotalInodes = %d, want 3", result.TotalInodes)
	}
	if result.HostSkipped != 0 {
		t.Errorf("HostSkipped = %d, want 0", result.HostSkipped)
	}
}

func TestProcScannerSkipsHostNetns(t *testing.T) {
	root := t.TempDir()

	pidToNsID := map[int]string{
		1:   "nsA",
		2:   "nsA",
		100: "nsB",
	}
	nsIDToInode := buildFakeProcTree(t, root, pidToNsID)
	hostInode := nsIDToInode["nsA"]

	var sweepCount int
	opener := &countingOpener{fn: func(_ string) { sweepCount++ }}

	scanner := New(
		opener,
		func() qdisc.Manager { return &fakeManager{links: nil} },
		false,
		silentLogger(),
		hostInode,
		root,
	)

	result, err := scanner.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() error: %v", err)
	}

	if sweepCount != 1 {
		t.Errorf("DoInNetns called %d times, want 1 (only nsB)", sweepCount)
	}
	if result.UniqueNetns != 1 {
		t.Errorf("UniqueNetns = %d, want 1", result.UniqueNetns)
	}
	if result.HostSkipped != 2 {
		t.Errorf("HostSkipped = %d, want 2 (both nsA entries)", result.HostSkipped)
	}
}

// A process that exits between ReadDir and Stat must not fail the sweep.
func TestProcScannerToleratesEnoent(t *testing.T) {
	root := t.TempDir()

	nsDir := filepath.Join(root, "_netns_files")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realNsFile := filepath.Join(nsDir, "nsA")
	if err := os.WriteFile(realNsFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(realNsFile, &st); err != nil {
		t.Fatal(err)
	}
	hostInode := uint64(999999999)

	pid100ns := filepath.Join(root, "100", "ns")
	if err := os.MkdirAll(pid100ns, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realNsFile, filepath.Join(pid100ns, "net")); err != nil {
		t.Fatal(err)
	}

	// Dangling link: pid 200 exited.
	pid200ns := filepath.Join(root, "200", "ns")
	if err := os.MkdirAll(pid200ns, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(nsDir, "gone"), filepath.Join(pid200ns, "net")); err != nil {
		t.Fatal(err)
	}

	var sweepCount int
	opener := &countingOpener{fn: func(_ string) { sweepCount++ }}

	scanner := New(
		opener,
		func() qdisc.Manager { return &fakeManager{links: nil} },
		false,
		silentLogger(),
		hostInode,
		root,
	)

	result, err := scanner.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() returned error on ENOENT: %v", err)
	}

	if sweepCount != 1 {
		t.Errorf("DoInNetns called %d times, want 1", sweepCount)
	}
	if result.TotalInodes != 1 {
		t.Errorf("TotalInodes = %d, want 1 (only stat-able entry)", result.TotalInodes)
	}
}

func TestProcScannerCountsReplacements(t *testing.T) {
	root := t.TempDir()

	pidToNsID := map[int]string{
		100: "nsA",
		200: "nsB",
	}
	buildFakeProcTree(t, root, pidToNsID)

	const hostInode = uint64(999999999)

	nsAPath := filepath.Join(root, "_netns_files", "nsA")
	nsBPath := filepath.Join(root, "_netns_files", "nsB")

	managerByFile := map[string]*fakeManager{
		nsAPath: {links: []qdisc.LinkInfo{{Name: "tap0_kata", RootQdiscType: "fq"}}},
		nsBPath: {links: []qdisc.LinkInfo{{Name: "eth0", RootQdiscType: "noqueue"}}},
	}

	opener := &resolveOpener{managerByFile: managerByFile}

	scanner := New(
		opener,
		opener.factory(),
		false,
		silentLogger(),
		hostInode,
		root,
	)

	result, err := scanner.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() error: %v", err)
	}

	if result.UniqueNetns != 2 {
		t.Errorf("UniqueNetns = %d, want 2", result.UniqueNetns)
	}
	if result.Replaced != 1 {
		t.Errorf("Replaced = %d, want 1", result.Replaced)
	}
	if result.TapsFound != 1 {
		t.Errorf("TapsFound = %d, want 1", result.TapsFound)
	}
}

type countingOpener struct {
	fn func(path string)
}

func (c *countingOpener) DoInNetns(path string, fn func() error) error {
	c.fn(path)
	return fn()
}

// resolveOpener "enters" a netns by resolving the ns/net link, so the factory
// can hand out the manager for that netns.
type resolveOpener struct {
	managerByFile map[string]*fakeManager
	current       string
}

func (r *resolveOpener) DoInNetns(path string, fn func() error) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	r.current = resolved
	return fn()
}

func (r *resolveOpener) factory() qdisc.ManagerFactory {
	return func() qdisc.Manager {
		mgr, ok := r.managerByFile[r.current]
		if !ok {
			return &fakeManager{links: nil}
		}
		return mgr
	}
}

type fakeManager struct {
	links []qdisc.LinkInfo
}

func (f *fakeManager) ListLinks() ([]qdisc.LinkInfo, error) {
	return append([]qdisc.LinkInfo(nil), f.links...), nil
}

func (f *fakeManager) ReplaceRootWithPfifoFast(string) error { return nil }
