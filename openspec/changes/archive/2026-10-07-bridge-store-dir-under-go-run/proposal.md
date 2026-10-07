## Why

The bridge always changes to the directory of its own executable so it uses the `store/` that the MCP server reads, whatever directory it is launched from. Under `go run`, though, Go builds the executable in a temporary folder (`$TMPDIR/go-build…/b001/exe/`), so the bridge creates an empty `store/` there: it shows a new QR code, links a second device and stores messages the MCP server never sees. `go run .` is the first thing many Go developers try, and the upstream README told people to use `go run main.go` for a year, so this is easy to hit and confusing to diagnose. The README now says to use `go build`, but the bridge should not depend on people reading that note.

## What Changes

- When the bridge detects it was started by `go run` (its executable sits in a `go-build…/…/exe` temporary folder, or, from Go 1.24, in a Go build cache entry reused on later runs), it uses the folder of its own source code instead, so `go run .` inside `whatsapp-bridge/` and `go -C whatsapp-bridge run .` from the repository root both use `whatsapp-bridge/store/`. (`go run ./whatsapp-bridge` from the root is not possible: the Go module lives in `whatsapp-bridge/`.)
- If the source folder cannot be determined (for example a `-trimpath` build), the bridge refuses to start under `go run` with a clear message, instead of silently creating a temporary store.
- The start-up output states which `store/` folder is in use.
- Built binaries keep today's behaviour: the `store/` next to the binary.
- README, AGENTS.md and CONTRIBUTING.md stop warning against `go run`, keeping `go build` as the recommended way to run the bridge day to day.

## Capabilities

### New Capabilities
- `bridge-store-location`: which `store/` folder the bridge uses, depending on how it was started, and how it reports it.

### Modified Capabilities
<!-- None: no existing spec covers the store location. -->

## Impact

- **Code**: `whatsapp-bridge/main.go` start-up (the `os.Executable`/`os.Chdir` block) moves into a small function in a new file with Go tests; no change to stored data, the REST API or the MCP tools.
- **Users**: `go run` starts working with the existing session. Anyone who already ran `go run` has an orphaned `store/` under their temporary folder; it is not migrated (the OS cleans it up), and its linked device can be removed from the phone.
- **Docs**: README (run steps and troubleshooting), AGENTS.md (gotcha), CHANGELOG.
