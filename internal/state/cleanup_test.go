package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btj93/herdr-tabline/internal/model"
)

// TestFailedWriteLeavesNoTemporaryFile exercises the cleanup path that runs only when a
// write fails after the temporary file exists. It is an internal test because the failure
// has to be injected at the rename, which is the last step and the only one that can fail
// with the temporary already on disk.
func TestFailedWriteLeavesNoTemporaryFile(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, filepath.Join(root, "herdr.sock"))

	original := commitRename
	injected := errors.New("injected rename failure")
	commitRename = func(string, string) error { return injected }
	t.Cleanup(func() { commitRename = original })

	_, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		tabs["w1:t1"] = model.LabelHistory{SourceLabel: "a", RenderedLabel: "1 a"}
		return true
	})
	if err == nil {
		t.Fatal("a failed rename was reported as success")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want the injected failure wrapped", err)
	}

	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file %q survived a failed write", entry.Name())
		}
	}
	// The state file itself must not have been created by the failed attempt.
	if _, statErr := os.Stat(store.Paths().State); !os.IsNotExist(statErr) {
		t.Fatalf("a failed write created the state file: %v", statErr)
	}
}
