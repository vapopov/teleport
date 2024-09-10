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
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gravitational/trace"

	"github.com/stretchr/testify/require"
)

const (
	testBinaryName = "updater"
)

var (
	testVersions = []string{
		"16.1.1",
		"17.1.2",
	}
)

func TestUpdate(t *testing.T) {
	// Create $TELEPORT_HOME/bin if it does not exist.
	dir, err := toolsDir()
	if err != nil {
		t.Fatalf("Failed to find tools directory: %v.", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("Failed to create tools directory: %v.", err)
	}

	err = update(testVersions[0])
	require.NoError(t, err)
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp(os.TempDir(), testBinaryName)
	if err != nil {
		log.Fatalf("Failed to create temporary directory: %v", err)
	}

	for _, version := range testVersions {
		if err := buildBinary(tmp, version); err != nil {
			log.Fatalf("Failed to build testing binary: %v", err)
		}
	}

	srv, address := startTestHTTPServer(tmp)
	baseUrl = fmt.Sprintf("http://%s", address)

	// Run tests after binary is built
	code := m.Run()

	if err := srv.Close(); err != nil {
		log.Fatalf("Failed to shutdown server: %v", err)
	}
	os.RemoveAll(tmp)

	os.Exit(code)
}

// serveFileHandler handles the serving of binary files and compression based on the extension.
func serveFileHandler(baseDir string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		filePath := filepath.Join(baseDir, r.URL.Path)

		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			serve256File(w, r, strings.TrimSuffix(filePath, ".sha256"))
		default:
			http.ServeFile(w, r, filePath)
		}
	}
}

// serve256File calculates sha256 checksum for requested file.
func serve256File(w http.ResponseWriter, _ *http.Request, filePath string) {
	log.Printf("Calculating and serving file as checksum: %s\n", filePath)

	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+".256\"")
	w.Header().Set("Content-Type", "plain/text")

	hash := sha256.New()
	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	if _, err := io.Copy(hash, file); err != nil {
		http.Error(w, "Failed to write to hash", http.StatusInternalServerError)
		return
	}
	if _, err := hex.NewEncoder(w).Write(hash.Sum(nil)); err != nil {
		http.Error(w, "Failed to write checksum", http.StatusInternalServerError)
	}
}

// generateZipFile compresses the file into a `.zip` format.
func generateZipFile(filePath, destPath string) error {
	archive, err := os.Create(destPath)
	if err != nil {
		return trace.Wrap(err)
	}
	defer archive.Close()

	zipWriter := zip.NewWriter(archive)
	defer zipWriter.Close()

	file, err := os.Open(filePath)
	if err != nil {
		return trace.Wrap(err)
	}
	defer file.Close()

	zipFileWriter, err := zipWriter.Create(filepath.Base(filePath))
	if err != nil {
		return trace.Wrap(err)
	}
	_, err = io.Copy(zipFileWriter, file)
	return trace.Wrap(err)
}

// generateTarGzFile compresses the file into a `.tar.gz` format and serves it.
func generateTarGzFile(filePath, destPath string) error {
	archive, err := os.Create(destPath)
	if err != nil {
		return trace.Wrap(err)
	}
	defer archive.Close()

	gzipWriter := gzip.NewWriter(archive)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Open source file for fetching content.
	file, err := os.Open(filePath)
	if err != nil {
		return trace.Wrap(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	// Create header info about the source file.
	header, err := tar.FileInfoHeader(info, info.Name())
	if err != nil {
		return err
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.Copy(tarWriter, file)
	return trace.Wrap(err)
}

// generatePkgFile runs the macOS `pkgbuild` command to generate a .pkg file from the source.
func generatePkgFile(filePath, destPath string) error {
	cmd := exec.Command("pkgbuild",
		"--root", filePath,
		"--identifier", "com.example.pkgtest",
		"--version", "1.0",
		destPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to generate .pkg: %s\n", output)
		return err
	}

	return nil
}

// startTestHTTPServer starts the file-serving HTTP server for testing.
func startTestHTTPServer(baseDir string) (*http.Server, string) {
	srv := &http.Server{Handler: http.HandlerFunc(serveFileHandler(baseDir))}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}

	go func() {
		if err := srv.Serve(listener); err != nil {
			log.Printf("Failed to start server: %s", err)
		}
	}()

	return srv, listener.Addr().String()
}

func buildBinary(path string, version string) error {
	output := filepath.Join(path, version, "tsh")
	if runtime.GOOS == "darwin" {
		output = filepath.Join(path, version, "tsh.app", "Contents", "MacOS", "tsh")
	}

	cmd := exec.Command(
		"go", "build", "-o", output,
		"-ldflags", fmt.Sprintf("-X 'main.Version=%s'", version),
		"./integration",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return trace.Wrap(err)
	}

	switch runtime.GOOS {
	case "darwin":
		return trace.Wrap(generatePkgFile(filepath.Join(path, version), path+"/tsh-"+version+".pkg"))
	case "windows":
		return trace.Wrap(generateZipFile(output, path+"/teleport-v"+version+"-windows-amd64-bin.zip"))
	case "linux":
		return trace.Wrap(generateTarGzFile(output, path+"/teleport-v"+version+"-linux-"+runtime.GOARCH+"-bin.tar.gz"))
	default:
		return trace.BadParameter("unsupported platform")
	}
}
