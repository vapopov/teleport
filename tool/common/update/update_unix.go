//go:build !windows

package update

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gravitational/trace"
)

func lock(dir string) (func(), error) {
	// Build the path to the lock file that will be used by flock.
	lockFile := filepath.Join(dir, ".lock")

	// Create the advisory lock using flock.
	lf, err := os.OpenFile(lockFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return nil, trace.Wrap(err)
	}

	return func() {
		if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); err != nil {
			slog.DebugContext(context.Background(), "failed to unlock file", "file", lockFile, "error", err)
		}
		//if err := os.Remove(lockFile); err != nil {
		//	log.Debugf("Failed to remove lock file: %v: %v.", lockFile, err)
		//}
		if err := lf.Close(); err != nil {
			slog.DebugContext(context.Background(), "failed to close lock file", "file", lockFile, "error", err)
		}
	}, nil
}
