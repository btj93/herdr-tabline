package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/state"
)

const (
	defaultSocket = "/home/me/.config/herdr/herdr.sock"
	namedSocket   = "/home/me/.config/herdr/sessions/work/herdr.sock"
)

func TestSessionKeySeparatesNamedSockets(t *testing.T) {
	a := state.SessionKey(defaultSocket)
	b := state.SessionKey(namedSocket)
	if a == b || len(a) != 16 || len(b) != 16 {
		t.Fatalf("keys %q %q", a, b)
	}
}

func TestSessionKeyIsStableAndPathNormalized(t *testing.T) {
	want := state.SessionKey(defaultSocket)
	for _, equivalent := range []string{
		"/home/me/.config/herdr/herdr.sock",
		"/home/me/.config/herdr/./herdr.sock",
		"/home/me/.config/herdr/sessions/../herdr.sock",
		"/home/me//.config/herdr/herdr.sock",
	} {
		if got := state.SessionKey(equivalent); got != want {
			t.Fatalf("SessionKey(%q) = %q, want %q", equivalent, got, want)
		}
	}
}

func TestPathsAreDistinctPerSocketAndKind(t *testing.T) {
	root := t.TempDir()
	first := state.NewPaths(root, defaultSocket)
	second := state.NewPaths(root, namedSocket)

	for _, group := range [][2]string{
		{first.State, second.State},
		{first.Lock, second.Lock},
		{first.PID, second.PID},
		{first.Log, second.Log},
	} {
		if group[0] == group[1] {
			t.Fatalf("sessions share a path: %q", group[0])
		}
	}
	kinds := map[string]string{"state": first.State, "lock": first.Lock, "pid": first.PID, "log": first.Log}
	seen := make(map[string]string, len(kinds))
	for kind, path := range kinds {
		if filepath.Dir(path) != root {
			t.Fatalf("%s path %q is not under the state root", kind, path)
		}
		if !strings.Contains(filepath.Base(path), first.Key) {
			t.Fatalf("%s path %q does not carry the session key %q", kind, path, first.Key)
		}
		if other, ok := seen[path]; ok {
			t.Fatalf("%s and %s share the path %q", kind, other, path)
		}
		seen[path] = kind
	}
}

func TestStoreRoundTripsTabsAndCreatesPrivateFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "created")
	store := state.NewStore(root, defaultSocket)

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("fresh store loaded %d entries", len(loaded))
	}

	changed, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		tabs["w1:t1"] = model.LabelHistory{SourceLabel: "shell", RenderedLabel: "1 shell"}
		tabs["w1:t2"] = model.LabelHistory{SourceLabel: "api", RenderedLabel: "2 api > codex"}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first write reported no change")
	}

	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]model.LabelHistory{
		"w1:t1": {SourceLabel: "shell", RenderedLabel: "1 shell"},
		"w1:t2": {SourceLabel: "api", RenderedLabel: "2 api > codex"},
	}
	if len(loaded) != len(want) {
		t.Fatalf("loaded %#v, want %#v", loaded, want)
	}
	for id, entry := range want {
		if loaded[id] != entry {
			t.Fatalf("tab %s = %#v, want %#v", id, loaded[id], entry)
		}
	}

	// A second store for the same socket must observe the same persisted state.
	if reloaded, err := state.NewStore(root, defaultSocket).Load(); err != nil {
		t.Fatal(err)
	} else if reloaded["w1:t2"].RenderedLabel != "2 api > codex" {
		t.Fatalf("independent store loaded %#v", reloaded)
	}

	paths := store.Paths()
	info, err := os.Stat(paths.State)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state file mode = %04o, want 0600", mode)
	}
	dir, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dir.Mode().Perm(); mode != 0o700 {
		t.Fatalf("state directory mode = %04o, want 0700", mode)
	}
}

func TestStoreWritesSchemaVersionAndRejectsOtherVersions(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(root, defaultSocket)
	if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		tabs["w1:t1"] = model.LabelHistory{SourceLabel: "a", RenderedLabel: "b"}
		return true
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		SchemaVersion int                           `json:"schema_version"`
		Tabs          map[string]model.LabelHistory `json:"tabs"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", file.SchemaVersion)
	}
	if file.Tabs["w1:t1"].SourceLabel != "a" {
		t.Fatalf("persisted tabs = %#v", file.Tabs)
	}

	if err := os.WriteFile(store.Paths().State, []byte(`{"schema_version":2,"tabs":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("Load error = %v, want a schema version complaint", err)
	}
}

func TestStoreReportsCorruptState(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(root, defaultSocket)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Paths().State, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(); err == nil {
		t.Fatal("Load accepted corrupt JSON")
	}
	if _, err := store.Update(func(map[string]model.LabelHistory) bool { return true }); err == nil {
		t.Fatal("Update silently reset corrupt JSON")
	}
	raw, err := os.ReadFile(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Fatalf("corrupt state was overwritten: %q", raw)
	}
}

func TestStorePurgesClosedTabs(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(root, defaultSocket)
	if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		tabs["w1:t1"] = model.LabelHistory{SourceLabel: "a"}
		tabs["w1:t2"] = model.LabelHistory{SourceLabel: "b"}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		delete(tabs, "w1:t1")
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("purge reported no change")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := loaded["w1:t1"]; present {
		t.Fatalf("closed tab survived the purge: %#v", loaded)
	}
	if loaded["w1:t2"].SourceLabel != "b" {
		t.Fatalf("surviving tab was disturbed: %#v", loaded)
	}
}

// TestStoreNoOpUpdatesDoNotReplaceTheFile is the guard for the atomic-write path:
// a no-op must not rename a temporary file over the state, because that would churn
// the inode and mtime on every quiet refresh.
func TestStoreNoOpUpdatesDoNotReplaceTheFile(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(root, defaultSocket)
	if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
		tabs["w1:t1"] = model.LabelHistory{SourceLabel: "a", RenderedLabel: "1 a"}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(store.Paths().State)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mutate func(map[string]model.LabelHistory) bool
	}{
		{name: "callback declines", mutate: func(map[string]model.LabelHistory) bool { return false }},
		{name: "callback rewrites identical values", mutate: func(tabs map[string]model.LabelHistory) bool {
			tabs["w1:t1"] = model.LabelHistory{SourceLabel: "a", RenderedLabel: "1 a"}
			return true
		}},
		{name: "callback deletes a tab that is absent", mutate: func(tabs map[string]model.LabelHistory) bool {
			delete(tabs, "w9:t9")
			return true
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			changed, err := store.Update(test.mutate)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Fatal("no-op update reported a change")
			}
			after, err := os.Stat(store.Paths().State)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("no-op update replaced the state file")
			}
			if !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("no-op update changed mtime: %v -> %v", before.ModTime(), after.ModTime())
			}
		})
	}
}

func TestStoreLeavesNoTemporaryFilesBehind(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(root, defaultSocket)
	for i := range 5 {
		if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
			tabs["w1:t1"] = model.LabelHistory{RenderedLabel: time.Duration(i).String()}
			return true
		}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := map[string]bool{
		filepath.Base(store.Paths().State): true,
		filepath.Base(store.Paths().Lock):  true,
	}
	for _, name := range names {
		if !want[name] {
			t.Fatalf("unexpected leftover file %q in %v", name, names)
		}
	}
}

// TestConcurrentStoresMergeRatherThanOverwrite proves each Update locks, reloads, and
// merges. Two stores holding independent in-memory views must not clobber each other.
func TestConcurrentStoresMergeRatherThanOverwrite(t *testing.T) {
	root := t.TempDir()
	const writers, perWriter = 4, 25

	var wait sync.WaitGroup
	errs := make(chan error, writers)
	for writer := range writers {
		wait.Add(1)
		go func(writer int) {
			defer wait.Done()
			store := state.NewStore(root, defaultSocket)
			for i := range perWriter {
				id := tabID(writer, i)
				if _, err := store.Update(func(tabs map[string]model.LabelHistory) bool {
					tabs[id] = model.LabelHistory{SourceLabel: id, RenderedLabel: id}
					return true
				}); err != nil {
					errs <- err
					return
				}
			}
		}(writer)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := state.NewStore(root, defaultSocket).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != writers*perWriter {
		t.Fatalf("merged %d entries, want %d", len(loaded), writers*perWriter)
	}
	for writer := range writers {
		for i := range perWriter {
			id := tabID(writer, i)
			if loaded[id].SourceLabel != id {
				t.Fatalf("entry %s lost: %#v", id, loaded[id])
			}
		}
	}
}

func tabID(writer, index int) string {
	return "w" + itoa(writer) + ":t" + itoa(index)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
