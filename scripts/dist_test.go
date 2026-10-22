// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package scripts_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDistArchives(t *testing.T) {
	repo := repoRoot(t)
	command(t, repo, "make", "dist")
	t.Cleanup(func() { command(t, repo, "make", "clean") })
	version := strings.TrimSpace(command(t, repo, "make", "-s", "print-version"))
	commit := strings.TrimSpace(command(t, repo, "make", "-s", "print-commit"))
	platforms := strings.Fields(command(t, repo, "make", "-s", "release-platforms"))
	dist := filepath.Join(repo, "dist")

	tests := make([]struct{ platform, archive string }, 0, len(platforms))
	for _, platform := range platforms {
		parts := strings.Split(platform, "/")
		if len(parts) != 2 {
			t.Fatalf("invalid platform %q", platform)
		}
		extension := ".tar.gz"
		if parts[0] == "windows" {
			extension = ".zip"
		}
		tests = append(tests, struct{ platform, archive string }{
			platform: platform,
			archive:  "ai-usage-exporter_" + version + "_" + parts[0] + "_" + parts[1] + extension,
		})
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(dist, tt.archive)); err != nil {
				t.Fatalf("expected archive %s: %v", tt.archive, err)
			}
			if strings.Contains(tt.archive, "windows") {
				if !strings.Contains(readFile(t, filepath.Join(repo, "docs", "install.md")), "ai-usage-exporter_<version>_<os>_<arch>.zip") {
					t.Fatal("install documentation no longer declares the Windows archive template")
				}
			} else if !strings.Contains(readFile(t, filepath.Join(repo, "docs", "install.md")), "ai-usage-exporter_<version>_<os>_<arch>.tar.gz") {
				t.Fatal("install documentation no longer declares the tarball archive template")
			}
		})
	}

	command(t, dist, "shasum", "-a", "256", "-c", "checksums.txt", "--ignore-missing")
	hostArchive := "ai-usage-exporter_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if runtime.GOOS == "windows" {
		hostArchive = "ai-usage-exporter_" + version + "_windows_" + runtime.GOARCH + ".zip"
	}
	binary := extractArchive(t, filepath.Join(dist, hostArchive))
	versionOutput := command(t, filepath.Dir(binary), binary, "version")
	for _, want := range []string{version, commit} {
		if !strings.Contains(versionOutput, want) {
			t.Errorf("extracted binary version output %q does not contain %q", versionOutput, want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir := scriptDir(t)
	return filepath.Dir(dir)
}

func command(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func extractArchive(t *testing.T, archive string) string {
	t.Helper()
	dir := t.TempDir()
	if strings.HasSuffix(archive, ".zip") {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			in, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			out, err := os.OpenFile(filepath.Join(dir, file.Name), os.O_CREATE|os.O_WRONLY, file.Mode())
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(out, in)
			_ = in.Close()
			_ = out.Close()
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, file.Name)
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if !header.FileInfo().Mode().IsRegular() {
				continue
			}
			path := filepath.Join(dir, header.Name)
			out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, header.FileInfo().Mode())
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(out, reader)
			_ = out.Close()
			if err != nil {
				t.Fatal(err)
			}
			return path
		}
	}
	t.Fatal("archive has no binary")
	return ""
}
