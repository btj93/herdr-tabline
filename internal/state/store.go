// Package state persists per-session tabline label history and owns the daemon lock.
//
// Every file is keyed by a hash of the Herdr socket path, so named sessions cannot share
// label state, PID locks, or logs by accident. Writes are atomic and skipped entirely when
// they would not change the stored bytes, because the daemon refreshes on a short interval
// and a quiet session must not churn the filesystem.
package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/btj93/herdr-tabline/internal/model"
)

// schemaVersion is the on-disk format version. A file carrying any other version is
// refused rather than migrated or reset, so a downgrade cannot silently discard state.
const schemaVersion = 1

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// File is the on-disk state document.
type File struct {
	SchemaVersion int                           `json:"schema_version"`
	Tabs          map[string]model.LabelHistory `json:"tabs"`
}

// Paths are the session-scoped file locations under a plugin state root.
type Paths struct {
	Root  string
	Key   string
	State string
	Lock  string
	PID   string
	Log   string
}

// SessionKey derives a stable, filesystem-safe identifier from a Herdr socket path.
// The path is normalized first so that equivalent spellings of one socket agree.
func SessionKey(socket string) string {
	sum := sha256.Sum256([]byte(normalizeSocket(socket)))
	return hex.EncodeToString(sum[:])[:16]
}

// NewPaths resolves every session-scoped file under root. The state lock is deliberately
// a separate file from the daemon PID lock: the state lock is taken briefly around each
// read-modify-write, while the PID lock is held for the whole daemon lifetime.
func NewPaths(root, socket string) Paths {
	key := SessionKey(socket)
	return Paths{
		Root:  root,
		Key:   key,
		State: filepath.Join(root, "labels-"+key+".json"),
		Lock:  filepath.Join(root, "labels-"+key+".lock"),
		PID:   filepath.Join(root, "daemon-"+key+".pid"),
		Log:   filepath.Join(root, "daemon-"+key+".log"),
	}
}

func normalizeSocket(socket string) string {
	if socket == "" {
		return ""
	}
	if !filepath.IsAbs(socket) {
		if absolute, err := filepath.Abs(socket); err == nil {
			return absolute
		}
	}
	return filepath.Clean(socket)
}

// Store reads and writes one session's label history.
type Store struct {
	paths Paths
}

func NewStore(root, socket string) *Store {
	return &Store{paths: NewPaths(root, socket)}
}

func (s *Store) Paths() Paths {
	return s.paths
}

// Load returns the persisted tabs. A missing state file is an empty session, not an error.
// It takes the state lock so it cannot observe a partially completed read-modify-write.
func (s *Store) Load() (map[string]model.LabelHistory, error) {
	release, err := s.lock(lockShared)
	if err != nil {
		return nil, err
	}
	defer release()
	tabs, _, err := s.read()
	return tabs, err
}

// Update applies mutate to the latest persisted tabs under an exclusive lock and writes
// the result only if the encoded bytes actually differ from what is on disk. The callback
// reports whether it intended a change; returning false skips the comparison entirely.
// The returned bool reports whether the state file was rewritten.
func (s *Store) Update(mutate func(map[string]model.LabelHistory) bool) (bool, error) {
	release, err := s.lock(lockExclusive)
	if err != nil {
		return false, err
	}
	defer release()

	tabs, current, err := s.read()
	if err != nil {
		return false, err
	}
	if !mutate(tabs) {
		return false, nil
	}
	encoded, err := encode(tabs)
	if err != nil {
		return false, err
	}
	if bytes.Equal(encoded, current) {
		return false, nil
	}
	if err := s.writeAtomically(encoded); err != nil {
		return false, err
	}
	return true, nil
}

// read returns the decoded tabs alongside the exact bytes on disk, so Update can compare
// against the real file rather than against a re-encoding of what it believes is there.
func (s *Store) read() (map[string]model.LabelHistory, []byte, error) {
	raw, err := os.ReadFile(s.paths.State)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]model.LabelHistory{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read state %q: %w", s.paths.State, err)
	}
	var file File
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, nil, fmt.Errorf("decode state %q: %w", s.paths.State, err)
	}
	if file.SchemaVersion != schemaVersion {
		return nil, nil, fmt.Errorf("state %q has schema version %d, want %d", s.paths.State, file.SchemaVersion, schemaVersion)
	}
	if file.Tabs == nil {
		file.Tabs = map[string]model.LabelHistory{}
	}
	return file.Tabs, raw, nil
}

func encode(tabs map[string]model.LabelHistory) ([]byte, error) {
	if tabs == nil {
		tabs = map[string]model.LabelHistory{}
	}
	// Marshal sorts map keys, so an unchanged set of tabs always encodes identically and
	// the no-op comparison in Update is stable across runs.
	encoded, err := json.Marshal(File{SchemaVersion: schemaVersion, Tabs: tabs})
	if err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	return append(encoded, '\n'), nil
}

func (s *Store) writeAtomically(encoded []byte) error {
	if err := os.MkdirAll(s.paths.Root, dirMode); err != nil {
		return fmt.Errorf("create state directory %q: %w", s.paths.Root, err)
	}
	temporary, err := os.CreateTemp(s.paths.Root, filepath.Base(s.paths.State)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	name := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(name)
		}
	}()

	if err := temporary.Chmod(fileMode); err != nil {
		return fmt.Errorf("set temporary state mode: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(name, s.paths.State); err != nil {
		return fmt.Errorf("replace state %q: %w", s.paths.State, err)
	}
	committed = true
	syncDir(s.paths.Root)
	return nil
}

// syncDir flushes the rename itself. A failure here is not fatal: the state is already
// visible to every reader on this machine, and the daemon rebuilds from a fresh snapshot
// if a crash loses it.
func syncDir(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer handle.Close()
	_ = handle.Sync()
}
