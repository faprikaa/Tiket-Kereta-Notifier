# Ubuntu Auto-Setup and Headless BookingKAI Design

## Status

Approved for implementation on 2026-08-03.

## Objective

Make Tiket-Kereta-Notifier straightforward to install and run on a native
Ubuntu host. A single idempotent setup command installs and verifies runtime
dependencies, downloads Go modules, and builds the application. BookingKAI
uses Chromium headlessly as its only fetch method.

## Scope

- Native Ubuntu on `amd64`.
- Go remains the application language.
- All existing providers remain supported.
- BookingKAI fetches only through Chromium in headless mode.
- Tiket.com continues to use the `curl_chrome110` curl-impersonate binary.
- Webhook mode continues to launch `cloudflared` from the Go process.
- Proxy endpoints remain externally managed and enter through `proxy_url`.
- The user starts and supervises the process manually with `tmux`.

## Non-Goals

- Docker or Docker Compose support.
- WARP installation or lifecycle management.
- systemd installation or process supervision.
- A rewrite from Go to Python.
- Automated CAPTCHA solving or techniques intended to evade site protections.
- Modifying secrets or generating `config.yml` automatically.

## Architecture

### Ubuntu setup module

The setup module is exposed through:

```bash
./scripts/setup-ubuntu.sh
./scripts/setup-ubuntu.sh --check
```

The default mode installs missing dependencies and builds the application.
`--check` performs the same validations without changing the host.

The setup implementation:

1. Confirms that the host is supported Ubuntu `amd64` and that `sudo` is
   available when root access is required.
2. Installs base packages, Chromium and its runtime libraries, fonts, CA
   certificates, and regular curl through the Ubuntu package manager.
3. Installs a Go toolchain compatible with the version declared by `go.mod`
   when the existing toolchain is missing or too old.
4. Downloads pinned `cloudflared` and curl-impersonate artifacts from their
   official release sources and verifies their checksums before installation.
5. Ensures `cloudflared`, `curl`, `curl_chrome110`, and Chromium resolve from
   `PATH` and prints their resolved paths and versions.
6. Runs `go mod download` and builds `./cmd` as
   `bin/tiket-kereta-notifier`.

Every step is idempotent. Existing compatible installations are reused.
Downloads use temporary files and are moved into place only after validation.
A failed step exits non-zero with the dependency name and a corrective action.

The Go application shells out to curl binaries and does not directly link
against libcurl. Any libcurl shared libraries required by the installed curl
packages are managed by Ubuntu's package manager.

### Runtime preflight module

A new preflight module hides dependency discovery behind one interface:

```go
Check(config *config.Config) error
```

The implementation checks only dependencies required by enabled features:

- Any BookingKAI provider requires a working Chromium binary.
- Any Tiket.com provider requires `curl_chrome110`.
- Enabled webhook mode requires `cloudflared`.

The returned error identifies every missing dependency in one report so the
user does not need multiple start-fail-fix cycles. Preflight never installs
software; installation remains the setup module's responsibility.

### BookingKAI browser module

The existing `BrowserQueue` remains the external seam used by BookingKAI
providers. Its implementation is simplified:

- Initialization launches one headless Chromium browser and one persistent
  stealth page shared by the serial queue.
- A configurable user-data directory preserves an authorized browser session
  across application restarts.
- `doFetch` invokes the Chromium path directly.
- HTTP, cloudscraper, and curl BookingKAI fetch implementations and their
  fallback selection are removed.
- Failure to initialize Chromium is a clear BookingKAI initialization error;
  it does not silently degrade to another fetch method.

The queue remains serial to avoid concurrent browser navigation, excessive
request rates, and shared-session races.

## Runtime Flow

1. Load and validate `config.yml`.
2. Run dependency preflight for the configured providers and webhook mode.
3. Initialize the shared BookingKAI browser queue when BookingKAI is enabled.
4. Warm up the persistent page and reuse its legitimate session state.
5. Start provider schedulers and Telegram polling or webhook mode.
6. Let the user supervise the process in `tmux`.

The README will document a manual run flow similar to:

```bash
tmux new -s tiket-bot
./bin/tiket-kereta-notifier -config config.yml
```

Detach and reattach instructions are included, but no wrapper controls tmux on
the user's behalf.

## Challenge and Error Handling

When BookingKAI returns a Cloudflare challenge or CAPTCHA page:

1. Classify the response as a challenge rather than a parsing failure.
2. Preserve diagnostic context without logging cookies, tokens, or page bodies
   containing sensitive session data.
3. Send a rate-limited Telegram notification.
4. Back off exponentially with a configured upper bound.
5. Retry only within a bounded policy; otherwise wait for the next scheduled
   check or manual intervention.

No automated CAPTCHA solver is included. A failure in one search is recorded
in provider status/history and does not terminate unrelated provider
schedulers.

## Configuration Changes

The existing `browser.headless` setting remains and the Ubuntu example sets it
to `true`. The browser configuration gains an optional persistent profile
directory:

```yaml
browser:
  headless: true
  chromium_path: ""
  user_data_dir: ".cache/tiket-kereta-notifier/chromium"
```

`display` and `xauthority` remain accepted for backward compatibility but are
not required in the documented Ubuntu headless path.

## Documentation

The README installation section will be replaced with:

- Supported Ubuntu architecture.
- One-command setup and non-mutating check commands.
- Dependency verification output and common corrections.
- Configuration copy/edit instructions.
- Manual tmux start, detach, reattach, and stop commands.
- A note that `127.0.0.1` in `proxy_url` refers to the Ubuntu host itself.
- Headless challenge behavior and manual-intervention expectations.

## Verification Strategy

### Go tests

- Preflight dependency requirements for each provider and webhook combination.
- Aggregated missing-dependency errors.
- BookingKAI `doFetch` selects Chromium directly.
- Browser initialization failure is surfaced without fallback.
- Challenge classification, notification rate limiting, and backoff bounds.
- Existing config remains compatible when `user_data_dir` is absent.

### Setup verification

- `bash -n scripts/setup-ubuntu.sh`.
- ShellCheck when available.
- `--check` is verified to perform no writes.
- Installer helper logic is exercised with mocked command discovery and
  download failures.
- A clean Ubuntu test host or VM runs setup twice; the second execution makes
  no unnecessary changes.

### Project verification

- `go test ./...`
- `go vet ./...`
- `go build -o bin/tiket-kereta-notifier ./cmd`
- Manual smoke test for Telegram startup, one non-browser provider, BookingKAI
  Chromium headless, and webhook startup when enabled.

## Acceptance Criteria

- A supported Ubuntu host can install requirements and build the bot with one
  setup command.
- Re-running setup succeeds without reinstalling compatible dependencies.
- `--check` reports readiness without changing the host.
- BookingKAI never attempts HTTP, cloudscraper, or curl fetch fallbacks.
- BookingKAI operates through Chromium with `browser.headless: true`.
- Missing feature-specific binaries are reported before schedulers start.
- No Docker, WARP, or systemd artifacts are added.
- README instructions allow the user to run the built binary manually in
  tmux.
