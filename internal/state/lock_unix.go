//go:build unix

package state

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type lockKind int

const (
	lockShared lockKind = iota
	lockExclusive
)

// lock takes the session's state lock and returns a release func. The lock file is
// separate from the daemon PID lock, so a short read-modify-write never contends with
// the daemon's lifetime lock. Waiting here is intentional: callers are coordinating a
// read-modify-write and must not proceed on stale data.
func (s *Store) lock(kind lockKind) (func(), error) {
	if err := os.MkdirAll(s.paths.Root, dirMode); err != nil {
		return nil, fmt.Errorf("create state directory %q: %w", s.paths.Root, err)
	}
	handle, err := os.OpenFile(s.paths.Lock, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("open state lock %q: %w", s.paths.Lock, err)
	}
	how := syscall.LOCK_SH
	if kind == lockExclusive {
		how = syscall.LOCK_EX
	}
	if err := flockRetry(handle, how); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("lock state %q: %w", s.paths.Lock, err)
	}
	return func() {
		_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		_ = handle.Close()
	}, nil
}

// flockRetry retries the interrupted-syscall case, which a signal-handling daemon can
// otherwise see as a spurious lock failure.
func flockRetry(handle *os.File, how int) error {
	for {
		err := syscall.Flock(int(handle.Fd()), how)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

// ErrDaemonRunning reports that another process already holds this session's daemon lock.
var ErrDaemonRunning = errors.New("a herdr-tabline daemon is already running for this session")

// heldLocks keeps every acquired daemon-lock file reachable for as long as the lock is
// held. os.File installs a finalizer that closes the descriptor, and closing it releases
// the flock — so a caller whose DaemonLock becomes unreachable after Acquire would have
// its lock silently dropped by the garbage collector, letting a second daemon start for
// the same session. Process-wide ownership is the right lifetime for a process-wide lock.
var (
	heldMu    sync.Mutex
	heldLocks = map[*os.File]struct{}{}
)

func retainLock(handle *os.File) {
	heldMu.Lock()
	defer heldMu.Unlock()
	heldLocks[handle] = struct{}{}
}

func releaseLock(handle *os.File) {
	heldMu.Lock()
	defer heldMu.Unlock()
	delete(heldLocks, handle)
}

// DaemonLock is the session's single-daemon guarantee. The flock is held on the PID file
// for the daemon's whole lifetime, so liveness is a property of the lock rather than of
// the recorded number — which is what makes stale-PID detection reliable.
type DaemonLock struct {
	paths Paths

	mu     sync.Mutex
	handle *os.File
}

func NewDaemonLock(root, socket string) *DaemonLock {
	return &DaemonLock{paths: NewPaths(root, socket)}
}

func (l *DaemonLock) Paths() Paths {
	return l.paths
}

// Acquire takes the session's daemon lock without blocking and records the current PID
// only after the lock is held, so a losing contender never overwrites the live PID.
func (l *DaemonLock) Acquire() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle != nil {
		return nil
	}
	if err := os.MkdirAll(l.paths.Root, dirMode); err != nil {
		return fmt.Errorf("create state directory %q: %w", l.paths.Root, err)
	}
	handle, err := os.OpenFile(l.paths.PID, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return fmt.Errorf("open daemon lock %q: %w", l.paths.PID, err)
	}
	if err := flockRetry(handle, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = handle.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w (%s)", ErrDaemonRunning, l.paths.PID)
		}
		return fmt.Errorf("lock daemon %q: %w", l.paths.PID, err)
	}
	if err := writePID(handle, os.Getpid()); err != nil {
		_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		_ = handle.Close()
		return err
	}
	retainLock(handle)
	l.handle = handle
	return nil
}

// Release drops the lock and clears the recorded PID. The file itself is truncated rather
// than removed: unlinking while holding the lock would let a successor lock a detached
// inode and believe it had won.
func (l *DaemonLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle == nil {
		return nil
	}
	handle := l.handle
	l.handle = nil
	releaseLock(handle)
	truncateErr := handle.Truncate(0)
	_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
	closeErr := handle.Close()
	if truncateErr != nil {
		return fmt.Errorf("clear daemon pid %q: %w", l.paths.PID, truncateErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close daemon lock %q: %w", l.paths.PID, closeErr)
	}
	return nil
}

// PID returns the recorded daemon PID, or 0 when no daemon has recorded one. A malformed
// PID file is reported as an error rather than guessed at.
func (l *DaemonLock) PID() (int, error) {
	raw, err := os.ReadFile(l.paths.PID)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read daemon pid %q: %w", l.paths.PID, err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("daemon pid %q is malformed: %q", l.paths.PID, text)
	}
	return pid, nil
}

// SignalStop sends SIGTERM to this session's daemon and reports whether it signaled one.
//
// A recorded PID is never trusted on its own, because PIDs are reused: the daemon is
// confirmed live by testing whether anybody still holds the session lock. If the lock is
// free the record is stale, and it is cleared without signaling — that is the case where
// a blind SIGTERM would hit an unrelated process. No daemon to stop is success.
func (l *DaemonLock) SignalStop() (bool, error) {
	pid, err := l.PID()
	if err != nil {
		return false, err
	}
	held, err := l.lockHeld()
	if err != nil {
		return false, err
	}
	if !held {
		if pid != 0 {
			if err := l.clearStalePID(); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if pid == 0 {
		// Locked but no PID recorded: a daemon is mid-startup between flock and write.
		return false, fmt.Errorf("daemon lock %q is held but records no pid", l.paths.PID)
	}
	if pid == os.Getpid() {
		return false, fmt.Errorf("refusing to signal this process as its own daemon (pid %d)", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, l.clearStalePID()
		}
		return false, fmt.Errorf("signal daemon %d: %w", pid, err)
	}
	return true, nil
}

// lockHeld reports whether some process holds this session's daemon lock. It probes with
// a non-blocking exclusive request on a separate descriptor and immediately unlocks, so a
// free lock is left exactly as it was found.
func (l *DaemonLock) lockHeld() (bool, error) {
	l.mu.Lock()
	ownedHere := l.handle != nil
	l.mu.Unlock()
	if ownedHere {
		return true, nil
	}
	handle, err := os.OpenFile(l.paths.PID, os.O_RDWR, fileMode)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open daemon lock %q: %w", l.paths.PID, err)
	}
	defer handle.Close()
	if err := flockRetry(handle, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, fmt.Errorf("probe daemon lock %q: %w", l.paths.PID, err)
	}
	_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
	return false, nil
}

func (l *DaemonLock) clearStalePID() error {
	handle, err := os.OpenFile(l.paths.PID, os.O_RDWR, fileMode)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open daemon pid %q: %w", l.paths.PID, err)
	}
	defer handle.Close()
	// Only clear while nobody holds the lock, so a daemon that starts between the probe
	// and here keeps its freshly written PID.
	if err := flockRetry(handle, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return fmt.Errorf("lock daemon pid %q: %w", l.paths.PID, err)
	}
	defer func() { _ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN) }()
	if err := handle.Truncate(0); err != nil {
		return fmt.Errorf("clear stale daemon pid %q: %w", l.paths.PID, err)
	}
	return nil
}

func writePID(handle *os.File, pid int) error {
	if err := handle.Truncate(0); err != nil {
		return fmt.Errorf("truncate daemon pid: %w", err)
	}
	if _, err := handle.Seek(0, 0); err != nil {
		return fmt.Errorf("rewind daemon pid: %w", err)
	}
	if _, err := handle.WriteString(strconv.Itoa(pid) + "\n"); err != nil {
		return fmt.Errorf("write daemon pid: %w", err)
	}
	return handle.Sync()
}
