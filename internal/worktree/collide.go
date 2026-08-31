package worktree

import (
	"path/filepath"
	"sort"
	"strings"
)

// Overlap is a pair of branches that changed some of the same files. Merging
// both is where conflicts come from.
type Overlap struct {
	BranchA string   `json:"branch_a"`
	BranchB string   `json:"branch_b"`
	Files   []string `json:"files"`
}

// MigrationClash is two branches introducing different migrations under the
// same sequence number — the failure that only shows up on the production
// database, after both branches have merged.
type MigrationClash struct {
	Number  string `json:"number"`
	BranchA string `json:"branch_a"`
	FileA   string `json:"file_a"`
	BranchB string `json:"branch_b"`
	FileB   string `json:"file_b"`
}

// HotFile is a file several in-flight branches all touch.
type HotFile struct {
	Path     string   `json:"path"`
	Branches []string `json:"branches"`
}

// RepoCollisions is the collision report for one repository.
type RepoCollisions struct {
	Repo       string           `json:"repo"`
	Base       string           `json:"base"`
	Branches   []string         `json:"branches"`
	Overlaps   []Overlap        `json:"overlaps"`
	Migrations []MigrationClash `json:"migrations"`
	Hot        []HotFile        `json:"hot_files"`
}

// Any reports whether anything worth showing was found.
func (r RepoCollisions) Any() bool {
	return len(r.Overlaps) > 0 || len(r.Migrations) > 0
}

// Collisions compares every branch that carries unmerged commits against the
// others, looking for work that will collide when both land.
func Collisions(repo *Repo) RepoCollisions {
	out := RepoCollisions{Repo: repo.Root, Base: repo.Base}
	if repo.Base == "" {
		return out
	}

	changed := map[string][]string{}
	for _, wt := range repo.Worktrees {
		if wt.Branch == "" || wt.Branch == repo.Base || wt.Primary && wt.Ahead == 0 {
			continue
		}
		if wt.HasCounts && wt.Ahead == 0 {
			continue // fully merged: nothing unique left to collide
		}
		files := changedFiles(repo.Root, repo.Base, wt.Branch)
		if len(files) == 0 {
			continue
		}
		changed[wt.Branch] = files
	}

	for b := range changed {
		out.Branches = append(out.Branches, b)
	}
	sort.Strings(out.Branches)

	byFile := map[string][]string{}
	for _, b := range out.Branches {
		for _, f := range changed[b] {
			byFile[f] = append(byFile[f], b)
		}
	}

	for i := 0; i < len(out.Branches); i++ {
		for j := i + 1; j < len(out.Branches); j++ {
			a, b := out.Branches[i], out.Branches[j]
			shared := intersect(changed[a], changed[b])
			if len(shared) > 0 {
				out.Overlaps = append(out.Overlaps, Overlap{BranchA: a, BranchB: b, Files: shared})
			}
		}
	}

	for f, branches := range byFile {
		if len(branches) >= 3 {
			sort.Strings(branches)
			out.Hot = append(out.Hot, HotFile{Path: f, Branches: branches})
		}
	}
	sort.Slice(out.Hot, func(i, j int) bool {
		if len(out.Hot[i].Branches) != len(out.Hot[j].Branches) {
			return len(out.Hot[i].Branches) > len(out.Hot[j].Branches)
		}
		return out.Hot[i].Path < out.Hot[j].Path
	})

	out.Migrations = migrationClashes(out.Branches, changed)
	return out
}

// migrationClashes finds identical sequence numbers used by different files.
func migrationClashes(branches []string, changed map[string][]string) []MigrationClash {
	type ref struct{ branch, file string }
	byNumber := map[string][]ref{}
	for _, b := range branches {
		for _, f := range changed[b] {
			num, ok := migrationNumber(f)
			if !ok {
				continue
			}
			byNumber[num] = append(byNumber[num], ref{branch: b, file: f})
		}
	}

	var out []MigrationClash
	for num, refs := range byNumber {
		for i := 0; i < len(refs); i++ {
			for j := i + 1; j < len(refs); j++ {
				if refs[i].branch == refs[j].branch || refs[i].file == refs[j].file {
					continue
				}
				out = append(out, MigrationClash{
					Number:  num,
					BranchA: refs[i].branch, FileA: refs[i].file,
					BranchB: refs[j].branch, FileB: refs[j].file,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

var migrationDirs = map[string]bool{
	"migration": true, "migrations": true, "migrate": true, "versions": true,
}

// migrationNumber returns the leading sequence number of a migration file.
func migrationNumber(path string) (string, bool) {
	segs := strings.Split(filepath.ToSlash(path), "/")
	if len(segs) < 2 {
		return "", false
	}
	found := false
	for _, s := range segs[:len(segs)-1] {
		if migrationDirs[strings.ToLower(s)] {
			found = true
			break
		}
	}
	if !found {
		return "", false
	}
	base := segs[len(segs)-1]
	i := 0
	for i < len(base) && base[i] >= '0' && base[i] <= '9' {
		i++
	}
	if i < 3 { // too short to be a sequence number (or a timestamp)
		return "", false
	}
	return strings.TrimLeft(base[:i], "0"), true
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	var out []string
	for _, s := range b {
		if set[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
