## 1. Bridge settings

- [x] 1.1 Add `send_guardrails.go` reading `WHATSAPP_READ_ONLY`, `WHATSAPP_SEND_RATE` (unset means no limit, `0` leaves a window unlimited) and `WHATSAPP_SEND_ALLOWED` once at start-up, and print one start-up line with read-only mode, rate limit and whether an allowlist is set (count only, never the entries); verify with table tests of the parsers (valid, invalid and empty values) in `send_guardrails_test.go`

## 2. Send guardrails

- [x] 2.1 Refuse `/api/send` with `403` in read-only mode before any other work; verify with an HTTP handler test that nothing is sent or stored
- [x] 2.2 Extend `validateMediaPath` to always refuse paths inside the bridge's `store/` and paths with a hidden component unless a `WHATSAPP_MEDIA_ROOTS` entry containing that component covers them (design D2); verify with table tests (store database, symlink into `store/`, `~/.ssh/id_ed25519`, `.env`, an ordinary document, an override root under a hidden folder)
- [x] 2.3 Add the recipient allowlist check (normalised numbers and JIDs, LID resolved as for storage) answering `403`; verify with tests for a listed person, a listed group and an unlisted number
- [x] 2.4 Add the sliding-window send limiter answering `429` with `Retry-After`, counting only sends that passed every other check; verify with tests using an injected clock (no limit when unset, 10th allowed and 11th refused with `10/60`, recovery after the window, `0` windows unlimited)

## 3. Listener guardrails

- [x] 3.1 Refuse listener create, update and test-delivery with `403` in read-only mode, keeping list, get, deliveries and delete; verify with API tests
- [x] 3.2 Change the webhook URL policy and the connection-time check per the modified `webhook-delivery` requirement (public hosts only when listed); verify by updating `webhook_policy_test.go` with the new scenarios (public host and host names refused by default, listed public accepted, public addresses refused at dial time without an allowlist) and checking existing local scenarios still pass
- [x] 3.3 Make deliveries of existing listeners that fail the new policy record a failure naming `WEBHOOK_ALLOWED_HOSTS`; verify with a delivery test

## 4. MCP server

- [x] 4.1 Add server instructions to `FastMCP("whatsapp", instructions=…)` per the `untrusted-content` spec; verify with a test that reads them from the server object
- [x] 4.2 Wrap message text in `format_message` with ID-carrying start and end markers, neutralise marker sequences inside the text, and add the one-line untrusted-content reminder to read tool outputs; verify with pytest cases including a forged end marker
- [x] 4.3 Skip registering the send tools, `create_listener`, `set_listener_enabled` and `test_listener` when `WHATSAPP_READ_ONLY` is set; verify with a test that lists the registered tools with and without the variable

## 5. Verification and docs

- [x] 5.1 Run `go vet ./...`, `go test -race ./...` and `uv run pytest`; verify all pass
- [x] 5.2 Live check with the user's go-ahead on recipient and text: one normal send to the user's own chat succeeds; a send of a file under `~/.ssh` is refused; with `WHATSAPP_SEND_RATE=1/5`, a second send within a minute is refused; with `WHATSAPP_READ_ONLY=true` on both processes, the send tools are missing in a new MCP session and `/api/send` answers `403`
- [x] 5.3 Add a README "Security" section (threat model, what each guardrail covers and does not, recommended setup: keep client approval for send and listener tools, read-only mode for summaries, `WHATSAPP_MEDIA_ROOTS`), add the new variables to the configuration table, update the webhook safety notes, and add the hard rules to AGENTS.md; verify the examples use fictitious data
- [x] 5.4 Before committing, add `CHANGELOG.md` bullets in UK English under the current CST date heading, marking the webhook default as BREAKING; verify the entry sits at the top of that date block
