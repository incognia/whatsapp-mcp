## 1. Locate the bridge folder

- [x] 1.1 Add `bridgeDir(exe, sourceFile string, exists func(string) bool) (dir string, goRun bool, err error)` in `whatsapp-bridge/store_dir.go` per design D1–D3; verify with table tests in `store_dir_test.go`: built binary (any working directory), macOS and Linux `go-build…/b001/exe` paths and Go build cache entries (`<2 hex>/<64 hex>-d`) with an absolute source path, a `-trimpath` relative source path (error), an absolute source path whose folder lacks `main.go`/`go.mod` (error), and a folder named `exe` outside any `go-build` parent (treated as built)
- [x] 1.2 Replace the `os.Executable`/`os.Chdir` block in `main()` with `bridgeDir`, exiting with the D3 message on error before any database is opened, and print `Using store: <absolute path>`; verify with `go vet ./...` and by reading the diff

## 2. Verification

- [x] 2.1 Run `go vet ./...` and `go test -race ./...` in `whatsapp-bridge/`; verify both pass
- [x] 2.2 With the running bridge stopped, start it with `go run .` inside `whatsapp-bridge/` a second time (cached executable), and then with `go -C whatsapp-bridge run .` from the repository root; verify each prints `Using store: …/whatsapp-bridge/store`, connects with no QR code, and that no `store/` appears under `$(go env GOTMPDIR)` or the system temporary folder's `go-build*` directories
- [x] 2.3 Run `go run -trimpath .` inside `whatsapp-bridge/`; verify it exits with the `go build` message and creates no `store/`
- [x] 2.4 Build with `go build -o whatsapp-bridge .` and start the binary from the repository root; verify it uses `whatsapp-bridge/store/` and connects as before

## 3. Docs

- [x] 3.1 Update the README (run steps: `go build` recommended, `go run .` also works; troubleshooting entry), AGENTS.md (gotcha) and CONTRIBUTING.md if it mentions `go run`; verify no doc still says `go run` creates a temporary store
- [x] 3.2 Before committing, add `CHANGELOG.md` bullets in UK English under the current CST date heading (a `fix:` for `go run` using the real store and failing closed otherwise, and a `docs:` for the updated instructions); verify the entry sits at the top of that date block
