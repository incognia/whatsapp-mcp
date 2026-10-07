## Context

See proposal.md (Why) and specs/bridge-store-location/spec.md. Today `main()` in `whatsapp-bridge/main.go` does:

```go
if exe, err := os.Executable(); err == nil {
    if exe, err = filepath.EvalSymlinks(exe); err == nil {
        if err := os.Chdir(filepath.Dir(exe)); err != nil { … }
    }
}
// later: os.MkdirAll("store", 0755), sqlite3 "file:store/whatsapp.db…", "store/messages.db"
```

Every store path after that is relative to the working directory. Probed on macOS with Go 1.26: under `go run`, `os.Executable()` is `/var/folders/…/T/go-build3835991174/b001/exe/<name>`; `runtime.Caller(0)` returns the absolute path of the source file (`…/whatsapp-bridge/main.go`), and with `-trimpath` a module-relative path (`whatsapp-client/main.go`).

## Goals / Non-Goals

**Goals:** `go run` in or outside `whatsapp-bridge/` uses `whatsapp-bridge/store/`; no silent temporary store ever; built binaries unchanged; the store path visible at start-up; the decision testable without starting the bridge.

**Non-Goals:** a configurable store location (`WHATSAPP_STORE_DIR`), which the MCP server would also have to learn; migrating stores already created under the temporary folder; `go test` binaries (they never run `main()`).

## Decisions

### D1. Detect `go run` from the executable's path
The executable's directory is treated as a `go run` build when either its base name is `exe` and one of its parent components starts with `go-build` (a fresh build in `$GOTMPDIR` or the system temporary folder), or it is a Go build cache entry: a `<64 hex>-d` folder inside a `<2 hex>` shard folder. Go 1.24+ caches `go run` executables and starts the cached copy on later runs without changes; found during the live check, where the second run created a store in `~/Library/Caches/go-build/15/…-d/` and showed a QR code before this rule was added (the empty store was removed, nothing was linked). Rejected: comparing with `os.TempDir()`, which misses a custom `GOTMPDIR`; checking `debug.ReadBuildInfo()`, which does not record whether the build was `go run`.

### D2. Source folder from `runtime.Caller`
Under `go run`, the bridge directory is `filepath.Dir` of the file reported by `runtime.Caller(0)` in the new file. It is accepted only if the path is absolute and the directory contains `main.go` and `go.mod`; otherwise start-up fails (D3). Rejected: using the working directory, which would also be the temporary folder for a cached executable and depends on how the user started it; walking up from the working directory looking for `whatsapp-bridge/`, which guesses and breaks for renamed clones.

### D3. Fail closed
If the source folder cannot be trusted, the bridge exits with `cannot locate the bridge folder under go run (…); build it with: go build -o whatsapp-bridge . && ./whatsapp-bridge`. A new temporary store means a new linked device and messages the MCP server never reads, which is worse than not starting.

### D4. One pure function plus a thin wrapper
`bridgeDir(exe, sourceFile string, exists func(string) bool) (dir string, goRun bool, err error)` holds the logic and is table-tested; `main()` passes `os.Executable()` (after `EvalSymlinks`), `runtime.Caller(0)` and a stat-based `exists`, then `os.Chdir(dir)` and prints `Using store: <dir>/store`. If `os.Executable` itself fails, today's behaviour stays (keep the working directory) but the store line still shows where it is.

## Risks / Trade-offs

- [Go changes the `go run` temporary layout] → detection stops matching, and the bridge falls back to today's behaviour (a temporary store), which the start-up line makes visible; the test table pins the current layout.
- [A built binary placed in a folder named `…/go-build…/exe/`] → would be treated as `go run`; harmless, because the source check then fails closed with a clear message.
- [Source path leaks the developer's directory into the binary] → already true for every non-`-trimpath` Go build; only used locally.

## Migration Plan

Rebuild and restart. Users who previously ran `go run` can remove the extra linked device on the phone (Settings › Linked devices). Rollback: revert the commit.
