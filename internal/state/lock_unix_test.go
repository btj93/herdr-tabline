//go:build unix

package state_test

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/btj93/herdr-tabline/internal/state"
)

func TestDaemonLockIsExclusivePerSessionAndNonBlocking(t *testing.T) {
	root := t.TempDir()
	held := state.NewDaemonLock(root, defaultSocket)
	if err := held.Acquire(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Release() })

	contender := state.NewDaemonLock(root, defaultSocket)
	start := time.Now()
	err := contender.Acquire()
	if err == nil {
		_ = contender.Release()
		t.Fatal("a second daemon acquired the same session lock")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Acquire blocked for %v, want a non-blocking failure", elapsed)
	}
	if !strings.Contains(err.Error(), "already") {
		t.Fatalf("contended Acquire error = %v, want it to name the running daemon", err)
	}

	// A different session must be unaffected by the held lock.
	other := state.NewDaemonLock(root, namedSocket)
	if err := other.Acquire(); err != nil {
		t.Fatalf("a different session could not acquire its own lock: %v", err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonLockWritesPIDOnlyAfterAcquiring(t *testing.T) {
	root := t.TempDir()
	lock := state.NewDaemonLock(root, defaultSocket)

	if pid, err := lock.PID(); err != nil || pid != 0 {
		t.Fatalf("PID before Acquire = (%d, %v), want (0, nil)", pid, err)
	}
	if _, err := os.Stat(lock.Paths().PID); err == nil {
		t.Fatal("PID file existed before Acquire")
	}

	if err := lock.Acquire(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	pid, err := lock.PID()
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Fatalf("recorded PID = %d, want %d", pid, os.Getpid())
	}
	info, err := os.Stat(lock.Paths().PID)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("PID file mode = %04o, want 0600", mode)
	}

	// A contender that fails to acquire must not overwrite the live PID.
	contender := state.NewDaemonLock(root, defaultSocket)
	if err := contender.Acquire(); err == nil {
		t.Fatal("contender acquired a held lock")
	}
	if again, err := lock.PID(); err != nil || again != pid {
		t.Fatalf("PID after a failed contender = (%d, %v), want (%d, nil)", again, err, pid)
	}
}

func TestDaemonLockReleaseAllowsReacquisition(t *testing.T) {
	root := t.TempDir()
	first := state.NewDaemonLock(root, defaultSocket)
	if err := first.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if pid, err := first.PID(); err != nil || pid != 0 {
		t.Fatalf("PID after Release = (%d, %v), want (0, nil)", pid, err)
	}

	second := state.NewDaemonLock(root, defaultSocket)
	if err := second.Acquire(); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestFinalizerCannotDropAHeldDaemonLock guards a hazard that is invisible in ordinary
// runs: os.File installs a finalizer that closes the descriptor, and closing it releases
// the flock. A caller that drops its DaemonLock reference after Acquire would therefore
// have the lock collected out from under it, admitting a second daemon for the session.
func TestFinalizerCannotDropAHeldDaemonLock(t *testing.T) {
	root := t.TempDir()
	func() {
		lock := state.NewDaemonLock(root, defaultSocket)
		if err := lock.Acquire(); err != nil {
			t.Fatal(err)
		}
	}() // reference deliberately dropped
	for range 5 {
		runtime.GC()
	}
	contender := state.NewDaemonLock(root, defaultSocket)
	if err := contender.Acquire(); err == nil {
		_ = contender.Release()
		t.Fatal("a held daemon lock was released by the garbage collector")
	}
}

func TestSignalStopReportsSuccessWhenNoDaemonRecorded(t *testing.T) {
	root := t.TempDir()
	lock := state.NewDaemonLock(root, defaultSocket)
	signaled, err := lock.SignalStop()
	if err != nil {
		t.Fatal(err)
	}
	if signaled {
		t.Fatal("SignalStop claimed to signal a daemon that was never started")
	}
}

// TestSignalStopRemovesStalePIDWithoutSignaling covers the PID-reuse hazard: a recorded
// PID whose lock nobody holds is stale, and must never be signaled.
func TestSignalStopRemovesStalePIDWithoutSignaling(t *testing.T) {
	root := t.TempDir()
	lock := state.NewDaemonLock(root, defaultSocket)

	// A live, unrelated process. It must survive, proving no blind SIGTERM was sent.
	victim := exec.Command("sleep", "30")
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = victim.Process.Kill()
		_, _ = victim.Process.Wait()
	})

	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := strconv.Itoa(victim.Process.Pid)
	if err := os.WriteFile(lock.Paths().PID, []byte(stale+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	signaled, err := lock.SignalStop()
	if err != nil {
		t.Fatal(err)
	}
	if signaled {
		t.Fatal("SignalStop signaled a PID whose session lock nobody holds")
	}
	if pid, err := lock.PID(); err != nil || pid != 0 {
		t.Fatalf("stale PID survived: (%d, %v)", pid, err)
	}
	if err := victim.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process was disturbed: %v", err)
	}
}

func TestSignalStopTerminatesTheLockHoldingDaemon(t *testing.T) {
	root := t.TempDir()
	socket := defaultSocket
	paths := state.NewPaths(root, socket)

	// A real child process that acquires the session lock and then waits, so the stop
	// path is exercised against a genuine flock holder rather than a stub.
	daemon := exec.Command(os.Args[0], "-test.run=TestHelperDaemonHoldsSessionLock")
	daemon.Env = append(os.Environ(),
		"HERDR_TABLINE_HELPER=1",
		"HERDR_TABLINE_HELPER_ROOT="+root,
		"HERDR_TABLINE_HELPER_SOCKET="+socket,
	)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- daemon.Wait() }()
	t.Cleanup(func() { _ = daemon.Process.Kill() })

	waitFor(t, "helper daemon to record its PID", func() bool {
		pid, err := state.NewDaemonLock(root, socket).PID()
		return err == nil && pid == daemon.Process.Pid
	})
	if _, err := os.Stat(paths.PID); err != nil {
		t.Fatal(err)
	}

	lock := state.NewDaemonLock(root, socket)
	signaled, err := lock.SignalStop()
	if err != nil {
		t.Fatal(err)
	}
	if !signaled {
		t.Fatal("SignalStop did not signal a live lock-holding daemon")
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit after SignalStop")
	}

	// With the daemon gone the lock is free again.
	successor := state.NewDaemonLock(root, socket)
	if err := successor.Acquire(); err != nil {
		t.Fatalf("lock was not released by the stopped daemon: %v", err)
	}
	if err := successor.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestHelperDaemonHoldsSessionLock is not a test. It is the child process body used by
// TestSignalStopTerminatesTheLockHoldingDaemon and exits immediately otherwise.
func TestHelperDaemonHoldsSessionLock(t *testing.T) {
	if os.Getenv("HERDR_TABLINE_HELPER") != "1" {
		t.Skip("helper process body")
	}
	lock := state.NewDaemonLock(os.Getenv("HERDR_TABLINE_HELPER_ROOT"), os.Getenv("HERDR_TABLINE_HELPER_SOCKET"))
	if err := lock.Acquire(); err != nil {
		os.Exit(3)
	}
	// Park on a timer rather than `select {}`. A bare select leaves the child with no
	// runnable goroutine and no pending timer, so the runtime's deadlock detector kills it
	// — which releases the lock and makes the parent's stop assertion flaky. The bound also
	// stops a leaked helper from outliving the run.
	time.Sleep(5 * time.Minute)
	os.Exit(4)
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
