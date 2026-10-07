# Contributing

Thanks for helping keep whatsapp-mcp alive. This fork welcomes bug reports, fixes and features, including work ported from upstream pull requests and other forks.

## Before you start

- **Small fixes** (typos, docs, an obvious bug with a test): open a pull request directly.
- **Anything that changes behaviour** (a new tool, endpoint, setting, schema change or a different default): open an issue first, or a pull request with only the OpenSpec proposal (see "Spec-driven changes"), so the approach can be agreed before the code is written.
- **Security problems**: do not open a public issue. Report them privately through GitHub's "Report a vulnerability" on this repository, with steps to reproduce.

## Privacy first

This project handles real people's private conversations. Every contribution must keep it that way:

- Never include real phone numbers, JIDs, names, message text or media in code, tests, fixtures, docs, issues, logs or screenshots. Use fictitious values such as `5215500000001`, `120363000000000000@g.us` and "Ana" or "Luis".
- Never commit anything from `whatsapp-bridge/store/`; it holds your message history and session key. It is ignored by Git, so do not force-add it.
- Do not log message content: the bridge's console output is metadata only unless `WHATSAPP_LOG_CONTENT` is set (see the `bridge-logging` spec).
- Keep the safe defaults: the REST API on loopback, media path checks, write-only webhook secrets and SSRF protections. A change that weakens one needs an explicit opt-in setting and a reason in its proposal.

## Development setup

Follow the [installation steps in the README](./README.md#installation) (Go 1.26+, Python 3.11+, uv). Then:

```bash
# Bridge
cd whatsapp-bridge
go build -o whatsapp-bridge .
go vet ./... && go test -race ./...

# MCP server
cd whatsapp-mcp-server
uv sync
uv run pytest
```

Both suites run without a WhatsApp connection. Python tests build temporary databases from fictitious data (`tests/conftest.py`); Go tests use temporary stores and injected dependencies instead of a live client.

To try a change live, link a test or secondary account if you can, run the rebuilt bridge and connect an MCP client to your clone.

## Spec-driven changes

Behaviour is specified with [OpenSpec](https://github.com/Fission-AI/OpenSpec) (spec-driven schema). The current behaviour of each capability lives in [`openspec/specs/`](./openspec/specs/); completed changes are kept in `openspec/changes/archive/`.

For any change in behaviour:

1. **Propose**: create `openspec/changes/<change-name>/` (kebab-case, for example `openspec new change quiet-message-content-logs`) with:
   - `proposal.md`: why, what changes and impact
   - `specs/<capability>/spec.md`: delta specs with `ADDED`, `MODIFIED`, `REMOVED` or `RENAMED` requirements, each with at least one `#### Scenario:` using WHEN/THEN
   - `design.md`: decisions and the alternatives rejected, risks and migration
   - `tasks.md`: small checkbox tasks, each saying how it is verified
2. **Validate**: `openspec validate <change-name> --strict` must pass.
3. **Implement**: work through `tasks.md`, ticking each task only when its behaviour is complete and verified. If the design turns out wrong, update the artefacts rather than drifting from them.
4. **Archive**: once merged, the delta specs are synced into `openspec/specs/` and the change moves to `openspec/changes/archive/YYYY-MM-DD-<change-name>/`.

Artefacts are written in English (UK) and keep the OpenSpec keywords (`SHALL`, `MUST`, `WHEN`, `THEN`) in English. Claude Code users can run the same flow with `/opsx:propose`, `/opsx:apply` and `/opsx:archive`; `.claude/` is not versioned, so generate those commands locally with `openspec init`. AI agents also follow [AGENTS.md](./AGENTS.md).

Docs-only changes, typo fixes and refactors with no change in behaviour do not need an OpenSpec change.

## Code guidelines

- Keep changes focused: one concern per pull request, matching the surrounding code's style, naming and comment density.
- **Go**: `gofmt`, `go vet` clean, no new data races (`-race`). Put new features in their own file (as `listeners.go` or `log_format.go` do) rather than growing `main.go`. Schema changes go through the versioned, additive migrations in `schema.go`; never drop or rewrite user data without a migration that keeps it.
- **Python**: follow the existing FastMCP tool style; tool docstrings are what the model reads, so describe arguments, limits and return values precisely.
- **Tests**: every fix gets a test that fails without it; every new requirement scenario should be covered by a test or a documented live check.
- **whatsmeow**: it is an unofficial client. Avoid features that generate unusual traffic (bulk sends, aggressive polling, mass history requests); rate-limit anything that talks to WhatsApp on its own.
- **Dependencies**: add as few as possible, and only with MIT-compatible licences.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) in English (UK):

```text
fix(bridge): keep message content out of the console by default

- Log live and sent messages as one metadata line instead of their text
- Add WHATSAPP_LOG_CONTENT to restore content for local debugging
```

- Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`
- Common scopes: `bridge`, `mcp`, `openspec`, `readme`
- Summary in the imperative, body as `-` bullets describing what changed and why
- If you used an AI assistant, credit it with a `Co-Authored-By:` trailer

## Changelog

Every pull request that changes code, docs or specs adds bullets to [CHANGELOG.md](./CHANGELOG.md):

- Under the heading for the current date in Mexico City time (`TZ=America/Mexico_City date +%Y-%m-%d`), creating `## [YYYY-MM-DD] - Short descriptive title` if it does not exist yet
- Newest bullets at the top of that date block, no blank lines between bullets
- Each bullet starts with its type (`fix:`, `feat:`, `docs:` …) and says what changed for the user, in English (UK)
- Only add lines; never rewrite earlier entries

## Credit and porting

- When you port a commit from upstream or another fork, cherry-pick it so the original author stays in the Git history, and mention the source pull request or fork in the CHANGELOG bullet (for example "upstream PR #350 by Matija Stepanic").
- When you reimplement an idea rather than copying code, say so ("approach from the LukasHaas fork").
- Add new contributors and sources to [AUTHORS.md](./AUTHORS.md) in the same pull request.

## Pull requests

- Branch from `main` and keep the branch up to date with it.
- Describe what changed, why, and how you verified it (tests run, live checks), and link the issue or OpenSpec change.
- Before asking for review, check that `go vet`, `go test -race`, `uv run pytest` and, if there is an OpenSpec change, `openspec validate --strict` all pass, and that the CHANGELOG has your bullets.

## Licence

This project is distributed under the [MIT License](./LICENSE). By contributing, you agree that your contributions are licensed under the same terms, and you keep the copyright of your own work. Only submit code you wrote or have the right to submit under the MIT License.
