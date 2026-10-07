# bridge-store-location Specification

## Purpose

Makes the bridge always use the `store/` folder inside `whatsapp-bridge/`, which the MCP server reads, however the bridge is started, so it never links a second device or writes messages where the MCP server cannot see them.

## Requirements

### Requirement: Built binaries use the store next to them
When the bridge runs from a built executable, it SHALL use the `store/` folder in the directory of that executable (after resolving symlinks), whatever the current working directory is.

#### Scenario: Started from another directory
- **WHEN** the binary `whatsapp-bridge/whatsapp-bridge` is started from the repository root
- **THEN** it uses `whatsapp-bridge/store/`

#### Scenario: Started through a symlink
- **WHEN** a symlink in another folder points to `whatsapp-bridge/whatsapp-bridge` and the bridge is started through it
- **THEN** it uses `whatsapp-bridge/store/`, not a `store/` next to the symlink

### Requirement: go run uses the source folder's store
When the bridge was started by `go run` (its executable is in a temporary `go run` build folder or in an entry of the Go build cache), it SHALL use the `store/` folder in the directory that contains the bridge's source code, regardless of the current working directory, and SHALL NOT create a `store/` in the temporary folder.

#### Scenario: go run inside the bridge folder
- **WHEN** a user runs `go run .` inside `whatsapp-bridge/` with an existing linked session
- **THEN** the bridge uses `whatsapp-bridge/store/`, connects with the existing session and shows no QR code

#### Scenario: Cached go run
- **WHEN** a user runs `go run .` a second time without code changes, so Go starts the executable it cached in its build cache
- **THEN** the bridge uses `whatsapp-bridge/store/` and shows no QR code

#### Scenario: go run from the repository root
- **WHEN** a user runs `go -C whatsapp-bridge run .` from the repository root
- **THEN** the bridge uses `whatsapp-bridge/store/`

### Requirement: Unknown source folder stops start-up
When the bridge was started by `go run` and the directory of its source code cannot be determined (the recorded source path is not absolute, or that directory does not contain the bridge's source), it SHALL exit before opening any database, with a message that names the problem and tells the user to build the bridge with `go build`.

#### Scenario: Trimmed paths
- **WHEN** a user runs `go run -trimpath .` inside `whatsapp-bridge/`
- **THEN** the bridge exits with a message recommending `go build -o whatsapp-bridge .`, and no `store/` folder is created anywhere

### Requirement: The store in use is reported
At start-up, before linking or connecting, the bridge SHALL print the absolute path of the `store/` folder it uses.

#### Scenario: Start-up line
- **WHEN** the bridge starts
- **THEN** its output includes the absolute path of the `store/` folder in use
