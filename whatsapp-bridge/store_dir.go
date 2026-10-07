package main

// Locating the bridge folder, whose store/ the MCP server reads. A built binary uses its own
// folder; under `go run` the binary lives in a temporary build folder, so the source folder is
// used instead, and start-up stops if that folder cannot be trusted.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// goCacheEntry matches a build cache entry folder (<2 hex>/<64 hex>-d), where Go 1.24+ keeps
// the executables of `go run` and reuses them on the next run
var goCacheEntry = regexp.MustCompile(`^[0-9a-f]{64}-d$`)
var goCacheShard = regexp.MustCompile(`^[0-9a-f]{2}$`)

// isGoRunBuild reports whether dir is a folder `go run` runs executables from: a fresh build
// (…/go-build…/…/exe) or a cached one (<GOCACHE>/<2 hex>/<64 hex>-d)
func isGoRunBuild(dir string) bool {
	if goCacheEntry.MatchString(filepath.Base(dir)) && goCacheShard.MatchString(filepath.Base(filepath.Dir(dir))) {
		return true
	}
	if filepath.Base(dir) != "exe" {
		return false
	}
	for parent := filepath.Dir(dir); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if strings.HasPrefix(filepath.Base(parent), "go-build") {
			return true
		}
	}
	return false
}

// bridgeDir returns the folder whose store/ the bridge must use. exe is the resolved path of the
// running executable and sourceFile the compile-time path of this file; exists reports whether a
// path exists. goRun is true when the executable was built by `go run`.
func bridgeDir(exe, sourceFile string, exists func(string) bool) (dir string, goRun bool, err error) {
	exeDir := filepath.Dir(exe)
	if !isGoRunBuild(exeDir) {
		return exeDir, false, nil
	}
	if !filepath.IsAbs(sourceFile) {
		return "", true, fmt.Errorf("source path %q is not absolute (built with -trimpath?)", sourceFile)
	}
	dir = filepath.Dir(sourceFile)
	if !exists(filepath.Join(dir, "main.go")) || !exists(filepath.Join(dir, "go.mod")) {
		return "", true, fmt.Errorf("source folder %s does not contain the bridge", dir)
	}
	return dir, true, nil
}

// useBridgeDir changes to the bridge folder and reports the store in use. It exits when the
// bridge was started by `go run` and its source folder cannot be located, before any database
// is opened, rather than creating a store in a temporary folder.
func useBridgeDir() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil {
		_, sourceFile, _, _ := runtime.Caller(0)
		exists := func(p string) bool { _, statErr := os.Stat(p); return statErr == nil }
		dir, _, dirErr := bridgeDir(exe, sourceFile, exists)
		if dirErr != nil {
			fmt.Fprintf(os.Stderr, "Cannot locate the bridge folder under go run (%v).\nBuild it instead: go build -o whatsapp-bridge . && ./whatsapp-bridge\n", dirErr)
			os.Exit(1)
		}
		if err := os.Chdir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to change to the bridge folder %s: %v\n", dir, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Cannot locate the bridge executable (%v); using the current folder\n", err)
	}

	if wd, err := os.Getwd(); err == nil {
		fmt.Printf("Using store: %s\n", filepath.Join(wd, "store"))
	}
}
