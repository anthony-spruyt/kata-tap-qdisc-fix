package procscan

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/netns"
	"github.com/anthony-spruyt/kata-tap-qdisc-fix/internal/qdisc"
)

type Result struct {
	// TotalInodes counts readable /proc/<pid>/ns/net entries, duplicates included.
	TotalInodes  int
	UniqueNetns  int
	HostSkipped  int
	TapsFound    int
	Replaced     int
	WouldReplace int
	Elapsed      time.Duration
}

// Scanner walks /proc/*/ns/net, deduplicates by netns inode, and applies the
// qdisc fix once in every netns other than the host's.
type Scanner struct {
	opener    netns.Opener
	factory   qdisc.ManagerFactory
	dryRun    bool
	logger    *slog.Logger
	hostInode uint64
	procRoot  string
}

// New returns a Scanner; an empty procRoot means "/proc".
func New(
	opener netns.Opener,
	factory qdisc.ManagerFactory,
	dryRun bool,
	logger *slog.Logger,
	hostInode uint64,
	procRoot string,
) *Scanner {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return &Scanner{
		opener:    opener,
		factory:   factory,
		dryRun:    dryRun,
		logger:    logger,
		hostInode: hostInode,
		procRoot:  procRoot,
	}
}

// HostNetnsInode stats /proc/<pid>/ns/net, or /proc/self/ns/net when pid is 0.
// Production passes 0: the runtime/default OCI spec masks /proc/<other>/ns/*,
// and with hostNetwork the daemon's own netns is the host's.
func HostNetnsInode(procRoot string, pid int) (uint64, error) {
	var path string
	if pid == 0 {
		path = filepath.Join(procRoot, "self", "ns", "net")
	} else {
		path = filepath.Join(procRoot, strconv.Itoa(pid), "ns", "net")
	}
	ino, err := inodeOf(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return ino, nil
}

func (s *Scanner) Sweep(ctx context.Context) (Result, error) {
	start := time.Now()
	var result Result

	entries, err := os.ReadDir(s.procRoot)
	if err != nil {
		return result, fmt.Errorf("readdir %s: %w", s.procRoot, err)
	}

	seen := make(map[uint64]string, 256)

	for _, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if !isPidEntry(entry) {
			continue
		}

		nsPath := filepath.Join(s.procRoot, entry.Name(), "ns", "net")
		inode, err := inodeOf(nsPath)
		if err != nil {
			s.logger.Debug("proc netns stat failed; process likely exited",
				"pid", entry.Name(), "error", err.Error())
			continue
		}

		result.TotalInodes++

		if inode == s.hostInode {
			result.HostSkipped++
			continue
		}

		if _, alreadySeen := seen[inode]; alreadySeen {
			continue
		}
		seen[inode] = nsPath
	}

	result.UniqueNetns = len(seen)

	for inode, nsPath := range seen {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		var res qdisc.Result
		err := s.opener.DoInNetns(nsPath, func() error {
			var applyErr error
			res, applyErr = qdisc.Apply(s.factory(), s.dryRun)
			return applyErr
		})
		if err != nil {
			s.logger.Debug("sweep: enter netns failed; skipping",
				"inode", inode, "path", nsPath, "error", err.Error())
			continue
		}

		result.TapsFound += res.Replaced + res.WouldReplace + res.Skipped
		result.Replaced += res.Replaced
		result.WouldReplace += res.WouldReplace

		if res.Replaced > 0 {
			s.logger.Info("proc sweep: qdisc replaced",
				"inode", inode, "path", nsPath, "replaced", res.Replaced)
		} else if res.WouldReplace > 0 {
			s.logger.Info("proc sweep: qdisc would replace (dry-run)",
				"inode", inode, "path", nsPath, "would_replace", res.WouldReplace)
		}
	}

	result.Elapsed = time.Since(start)
	return result, nil
}

func isPidEntry(e os.DirEntry) bool {
	if !e.IsDir() {
		return false
	}
	name := e.Name()
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return name != ""
}

// inodeOf uses Stat, not Lstat: the inode behind the ns/net symlink identifies the netns.
func inodeOf(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return st.Ino, nil
}
