package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiskCountsAndAgesShellSnapshots(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	snaps := filepath.Join(dir, "shell-snapshots")
	if err := os.MkdirAll(snaps, 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(snaps, "new.sh")
	stale := filepath.Join(snaps, "old.sh")
	if err := os.WriteFile(fresh, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, make([]byte, 900), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	parts := Disk()
	if len(parts) != 1 || parts[0].Name != "shell snapshots" {
		t.Fatalf("expected one measured part, got %+v", parts)
	}
	p := parts[0]
	if p.Bytes != 1000 || p.Files != 2 {
		t.Errorf("size = %d over %d files, want 1000 over 2", p.Bytes, p.Files)
	}
	// Only the old one is dead weight; a fresh snapshot belongs to a session
	// that may still be running.
	if p.Stale != 900 || p.StaleFiles != 1 {
		t.Errorf("stale = %d over %d files, want 900 over 1", p.Stale, p.StaleFiles)
	}

	total, staleBytes := TotalDisk(parts)
	if total != 1000 || staleBytes != 900 {
		t.Errorf("totals = %d/%d, want 1000/900", total, staleBytes)
	}
}

func TestDiskIgnoresWhatIsNotThere(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if parts := Disk(); len(parts) != 0 {
		t.Errorf("an empty directory measures nothing, got %+v", parts)
	}
	total, staleBytes := TotalDisk(nil)
	if total != 0 || staleBytes != 0 {
		t.Errorf("nothing totals to nothing, got %d/%d", total, staleBytes)
	}
}
