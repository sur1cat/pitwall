package worktree

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// configPatterns are the files a checkout needs but git deliberately does not
// carry: local environment and credentials. A fresh worktree has none of them,
// which is why a new agent's first command so often fails.
var configPatterns = []string{
	".env", ".env.*", "*.env",
	".envrc", ".tool-versions", ".nvmrc", ".npmrc", ".yarnrc", ".yarnrc.yml",
	"config.local.*", "*.local.json", "*.local.yml", "*.local.yaml", "*.local.toml",
	"local.settings.json", "secrets.yml", "secrets.yaml",
	".claude/settings.local.json",
}

// maxConfigBytes keeps this to configuration, never data or dependencies.
const maxConfigBytes = 512 * 1024

// PrepItem is one file considered for copying into a worktree.
type PrepItem struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Copied  bool   `json:"copied"`
	Skipped string `json:"skipped,omitempty"`
}

// Prep copies the local configuration a worktree is missing from its main
// checkout. It only touches files git ignores, never overwrites anything that
// already exists, and never copies directories.
func Prep(worktreePath string, dryRun bool) ([]PrepItem, error) {
	worktreePath = Resolve(worktreePath)
	root, err := repoRoot(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository", worktreePath)
	}
	if Resolve(root) == worktreePath {
		return nil, fmt.Errorf("%s is the main checkout, not a worktree", worktreePath)
	}

	candidates := ignoredConfigFiles(root)
	var items []PrepItem
	for _, rel := range candidates {
		src := filepath.Join(root, rel)
		dst := filepath.Join(worktreePath, rel)
		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			continue
		}
		item := PrepItem{Path: rel, Bytes: info.Size()}
		switch {
		case info.Size() > maxConfigBytes:
			item.Skipped = "larger than a config file should be"
		case exists(dst):
			item.Skipped = "already there"
		case dryRun:
			// nothing to do; the caller only wants the plan
		default:
			if err := copyFile(src, dst, info.Mode()); err != nil {
				item.Skipped = err.Error()
			} else {
				item.Copied = true
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// ignoredConfigFiles lists the git-ignored files in a checkout that look like
// local configuration.
func ignoredConfigFiles(root string) []string {
	out := gitOut(root, "ls-files", "--others", "--ignored", "--exclude-standard",
		"--directory", "--no-empty-directory")
	var found []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		rel := strings.TrimSpace(line)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue // a whole ignored directory is dependencies, not config
		}
		if !looksLikeConfig(rel) || seen[rel] {
			continue
		}
		seen[rel] = true
		found = append(found, rel)
	}
	return found
}

func looksLikeConfig(rel string) bool {
	base := filepath.Base(rel)
	for _, pattern := range configPatterns {
		if strings.Contains(pattern, "/") {
			if ok, _ := filepath.Match(pattern, filepath.ToSlash(rel)); ok {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
