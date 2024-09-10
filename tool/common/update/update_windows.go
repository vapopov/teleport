//go:build windows

/*
 * Teleport
 * Copyright (C) 2024  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package update

import (
	"archive/zip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gravitational/trace"
)

var (
	kernel    = windows.NewLazyDLL("kernel32.dll")
	proc      = kernel.NewProc("CreateFileW")
	ErrLocked = trace.BadParameter("update is locked by another process")
)

func replace(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return trace.Wrap(err)
	}
	fi, err := f.Stat()
	if err != nil {
		return trace.Wrap(err)
	}
	zipReader, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return trace.Wrap(err)
	}

	dir, err := toolsDir()
	if err != nil {
		return trace.Wrap(err)
	}
	tempDir, err := os.MkdirTemp(dir, "temp-tools-dir")
	if err != nil {
		return trace.Wrap(err)
	}

	for _, r := range zipReader.File {
		// Skip over any files in the archive that are not {tsh, tctl}.
		if r.Name != "tsh.exe" && r.Name != "tctl.exe" {
			continue
		}

		rr, err := r.Open()
		if err != nil {
			return trace.Wrap(err)
		}
		defer rr.Close()

		//dest := filepath.Join(dir, strings.TrimPrefix(header.Name, "teleport/"))
		dest := filepath.Join(dir, r.Name)
		t, err := os.CreateTemp(tempDir, dest)
		if err != nil {
			return trace.Wrap(err)
		}
		if err := os.Chmod(t.Name(), 0755); err != nil {
			return trace.Wrap(err)
		}

		if _, err := io.Copy(t, rr); err != nil {
			return trace.Wrap(err)
		}

		//if err := windows.Rename(t.Name(), rr); err != nil {
		//	return trace.Wrap(err)
		//}
		// windows.SYMBOLIC_LINK_FLAG_DIRECTORY
		// windows.MOVEFILE_REPLACE_EXISTING

		//if err := t.CloseAtomicallyReplace(); err != nil {
		//	return trace.Wrap(err)
		//}
	}
	return nil
}

func lock(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	lockPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	var lockFile *os.File
	fd, _, err := proc.Call(
		uintptr(unsafe.Pointer(lockPath)),
		uintptr(windows.GENERIC_READ|windows.GENERIC_WRITE),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE),
		uintptr(0),
		uintptr(windows.OPEN_ALWAYS),
		uintptr(windows.FILE_ATTRIBUTE_NORMAL),
		0,
	)
	switch err.(windows.Errno) {
	case windows.NO_ERROR, windows.ERROR_ALREADY_EXISTS:
		lockFile = os.NewFile(fd, path)
	case windows.ERROR_SHARING_VIOLATION:
		return nil, ErrLocked
	default:
		windows.CloseHandle(windows.Handle(fd))
		return nil, trace.Wrap(err)
	}
	if err := windows.SetHandleInformation(windows.Handle(lockFile.Fd()), windows.HANDLE_FLAG_INHERIT, 1); err != nil {
		return nil, trace.Wrap(err)
	}

	return func() {
		//if err := os.Remove(lockFile); err != nil {
		//	log.Debugf("Failed to remove lock file: %v: %v.", lockFile, err)
		//}
		if err := lockFile.Close(); err != nil {
			slog.DebugContext(context.Background(), "failed to close lock file", "file", lockFile, "error", err)
		}
	}, nil
}
