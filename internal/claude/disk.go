package claude

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DiskUse is what one part of Claude Code's own state occupies.
type DiskUse struct {
	Name  string
	Path  string
	Bytes int64
	Files int
	// Stale is the part belonging to sessions that are gone, which is dead
	// weight rather than history.
	Stale int64
	// StaleFiles counts the files behind Stale.
	StaleFiles int
	// Why explains what the stale part is, when there is one.
	Why string
}

// staleSnapshotAge is when a shell snapshot stops being worth keeping. It
// records the shell environment of one session, so once that session is long
// gone it reproduces nothing.
const staleSnapshotAge = 7 * 24 * time.Hour

// Disk measures what Claude Code keeps on disk, largest first.
//
// It is not small — 2.3 GB here — and nothing reports it. The worktree scan
// accounts for what agents leave in repositories and says nothing about what
// Claude Code leaves in its own directory, which on this machine is the same
// order of magnitude.
func Disk() []DiskUse {
	root := Dir()
	parts := []struct{ name, dir, why string }{
		{"transcripts", "projects", ""},
		{"file history", "file-history", ""},
		{"shell snapshots", "shell-snapshots", "snapshots of a session's shell, useless once the session is gone"},
		{"tasks", "tasks", ""},
		{"pasted content", "paste-cache", ""},
		{"backups", "backups", ""},
		{"plans", "plans", ""},
	}
	var out []DiskUse
	for _, p := range parts {
		dir := filepath.Join(root, p.dir)
		u := DiskUse{Name: p.name, Path: dir, Why: p.why}
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			u.Bytes += info.Size()
			u.Files++
			// Only shell snapshots go stale on age alone; the rest are pruned
			// by Claude Code alongside the transcripts they belong to.
			if p.dir == "shell-snapshots" && time.Since(info.ModTime()) > staleSnapshotAge {
				u.Stale += info.Size()
				u.StaleFiles++
			}
			return nil
		})
		if u.Files > 0 {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// TotalDisk is everything the parts add up to.
func TotalDisk(u []DiskUse) (total, stale int64) {
	for _, d := range u {
		total += d.Bytes
		stale += d.Stale
	}
	return total, stale
}
