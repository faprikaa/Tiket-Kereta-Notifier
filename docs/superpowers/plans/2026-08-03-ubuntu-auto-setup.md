# Ubuntu Auto-Setup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an idempotent Ubuntu installer, feature-aware runtime preflight, and Chromium-only headless BookingKAI flow.

**Architecture:** Keep Go and hide executable discovery in a preflight module. Keep `BrowserQueue` as the BookingKAI seam, remove alternate fetch adapters, persist Chromium state with Rod `UserDataDir`, and surface launch errors. Bash owns host mutation and builds the binary for manual tmux operation.

**Tech Stack:** Go 1.25.5, go-rod v0.116.2, Bash, Ubuntu apt, Chromium, cloudflared, curl-impersonate.

**Execution override:** The user explicitly requested implementation without
tests or verification. Test and verification steps below document the intended
quality path but are skipped for this execution; the pushed result is
unverified.

---

### Task 1: Feature-aware runtime preflight

**Files:**
- Create: `internal/preflight/preflight.go`
- Create: `internal/preflight/preflight_test.go`
- Modify: `cmd/main.go`

- [ ] **Step 1: Write failing tests for BookingKAI, Tiket.com, webhook, custom Chromium path, and aggregated errors**

```go
func TestCheckWithAggregatesMissingDependencies(t *testing.T) {
    cfg := &config.Config{Webhook: config.WebhookConfig{Enabled: true}, FlatTrains: []config.FlatTrainConfig{{ProviderName: "bookingkai"}, {ProviderName: "tiketcom"}}}
    err := checkWith(cfg, func(string) (string, error) { return "", exec.ErrNotFound }, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist })
    for _, dependency := range []string{"chromium", "curl_chrome110", "cloudflared"} {
        if !strings.Contains(err.Error(), dependency) { t.Fatalf("missing %s in %v", dependency, err) }
    }
}
```

- [ ] **Step 2: Add `Check` and injected `checkWith` implementation**

```go
func Check(cfg *config.Config) error {
    return checkWith(cfg, exec.LookPath, os.Stat)
}
```

The implementation deduplicates provider requirements, checks a configured Chromium path before PATH candidates, sorts missing dependency messages, and returns one error.

- [ ] **Step 3: Call preflight after config validation and before Telegram/browser initialization**

```go
if err := preflight.Check(cfg); err != nil {
    logger.Error("Runtime dependency check failed", "error", err)
    os.Exit(1)
}
```

### Task 2: Chromium-only BookingKAI

**Files:**
- Create: `internal/bookingkai/queue_test.go`
- Modify: `internal/bookingkai/queue.go`
- Modify: `internal/config/config.go`
- Modify: `cmd/main.go`

- [ ] **Step 1: Write tests proving `doFetch` invokes only the browser adapter and challenge backoff is bounded**

```go
func TestDoFetchUsesBrowserOnly(t *testing.T) {
    called := 0
    q := &BrowserQueue{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), browserFetch: func(context.Context, string) ([]common.Train, error) { called++; return []common.Train{{Name: "BENGAWAN"}}, nil }}
    trains, method, err := q.doFetch(context.Background(), "https://example.test")
    if err != nil || method != "browser" || called != 1 || len(trains) != 1 { t.Fatalf("unexpected result: %v %s %d %d", err, method, called, len(trains)) }
}
```

- [ ] **Step 2: Remove BookingKAI HTTP, curl, and cloudscraper adapters and dependencies**

`doFetch` calls the injected browser adapter (or `fetchViaBrowser`) directly. Challenge errors update bounded exponential backoff state and rate-limit Telegram notifications.

- [ ] **Step 3: Persist Chromium data and surface launch failures**

```go
if userDataDir != "" {
    if err := os.MkdirAll(userDataDir, 0o700); err != nil { return nil, fmt.Errorf("create Chromium user data directory: %w", err) }
    l = l.UserDataDir(userDataDir)
}
```

Change `NewBrowserQueue` to return `(*BrowserQueue, error)` and update `initAllProviders` accordingly.

- [ ] **Step 4: Add `browser.user_data_dir` and keep legacy display fields compatible**

```go
UserDataDir string `yaml:"user_data_dir"`
```

### Task 3: Idempotent Ubuntu setup module

**Files:**
- Create: `scripts/setup-ubuntu.sh`

- [ ] **Step 1: Add strict shell structure and non-mutating `--check` mode**

```bash
#!/usr/bin/env bash
set -Eeuo pipefail
MODE=install
[[ "${1:-}" == "--check" ]] && MODE=check
```

- [ ] **Step 2: Add Ubuntu/amd64 validation and dependency probes**

Check `curl`, `chromium-browser|chromium`, `cloudflared`, `curl_chrome110`, and a Go version compatible with `go.mod`.

- [ ] **Step 3: Install missing apt packages and pinned verified artifacts**

Install CA certificates, curl, Chromium runtime dependencies, fonts, NSS, tar, xz, and tmux. Download pinned cloudflared and curl-impersonate releases to a temporary directory, verify SHA-256, then install atomically.

- [ ] **Step 4: Download Go modules and build only in install mode**

```bash
go mod download
mkdir -p bin
go build -o bin/tiket-kereta-notifier ./cmd
```

### Task 4: Configuration and operator documentation

**Files:**
- Modify: `config.yml.example`
- Modify: `config.schema.json`
- Modify: `README.md`

- [ ] **Step 1: Set headless Chromium and a persistent profile in examples and schema**

```yaml
browser:
  headless: true
  chromium_path: ""
  user_data_dir: ".cache/tiket-kereta-notifier/chromium"
```

- [ ] **Step 2: Document setup/check commands and tmux lifecycle**

Document setup, config copy, `tmux new -s tiket-bot`, detach, attach, and stop. State that Docker, WARP, systemd, and automated CAPTCHA solving are not included.

### Task 5: Static verification, commit, and push

**Files:** All changed files.

- [ ] **Step 1: Run allowed static checks**

```bash
bash -n scripts/setup-ubuntu.sh
git diff --check
```

Go tests, vet, formatting, and build are intentionally not executed per user instruction; report this limitation explicitly.

- [ ] **Step 2: Inspect the complete diff and exclude `.codebase-memory/`**

```bash
git status --short
git diff --stat
```

- [ ] **Step 3: Commit and push `main`**

```bash
git add cmd internal scripts config.yml.example config.schema.json README.md docs/superpowers/plans/2026-08-03-ubuntu-auto-setup.md
git commit -m "feat: add Ubuntu auto-setup"
git push origin main
```
