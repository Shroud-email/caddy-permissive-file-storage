# E2E Test for caddy-permissive-file-storage

## Problem

The plugin's `Store()` override drifted from the `certmagic.Storage` interface
(`Store(key, value)` vs. the modern `Store(ctx, key, value)`) and nothing caught
it for years. The repo has zero tests; CI only builds a Docker image against a
pinned ancient Caddy (2.4.5), which is why the drift went unnoticed. We need a
test that would have failed at PR time and that guards the plugin's actual
contract going forward.

## Goal

A single end-to-end test that builds a real Caddy binary with the local plugin,
runs it, causes a certificate to be written through the plugin's `Store()`
override, and asserts the on-disk permissions match the plugin's contract
(files `0644`, dirs `0755`).

## Non-goals

- Testing ACME issuance end-to-end (Pebble, DNS-01, rate limits). Overkill —
  `tls internal` exercises the same `cfg.Storage.Store` path.
- Testing the plugin in isolation as a unit (no Caddy binary). A separate
  compile-check may be added later, but this spec is the E2E.
- Cross-platform permission semantics. The test targets Linux (CI) and macOS
  (local dev); both honor the mode bits we assert on.

## Design

### Approach: full binary E2E with `tls internal`, as a bash script

A bash script (`test/e2e.sh`) — not a Go `_test.go` — because the test is
fundamentally a shell pipeline (build a binary, run a process, make an HTTPS
request, inspect the filesystem). A Go test would wrap `os/exec` around these
same commands with no Go-level benefit: we never inspect Go objects, only files
and a process. Bash also makes the permission assertions particularly readable
(`find ... ! -perm 0644`). Putting it in a committed script (rather than inline
in the workflow YAML) means CI and local devs run the exact same thing:
`bash test/e2e.sh`.

The script:

1. **Skips early** if `xcaddy` is not on `PATH` (prints a message and exits 0),
   so local devs without the toolchain aren't blocked.
2. **Builds** a Caddy binary into a temp dir via
   `xcaddy build v2.10.0 --with github.com/Shroud-email/caddy-permissive-file-storage=.`
   so it compiles the **local working copy**, not a fetched version.
3. **Writes** a Caddyfile in a temp dir configuring our storage module and the
   `tls internal` issuer for `localhost`.
4. **Starts** `caddy run --config <Caddyfile>` as a background subprocess
   (`&`). Readiness is signaled by the process staying alive past a short grace
   period — Caddy exits non-zero on config parse failure. The admin API is
   disabled (`admin off`, see Caddyfile) to avoid port collisions.
5. **Triggers** cert issuance with `curl -sk https://localhost:<https_port>/`
   (the internal cert is self-signed, so `-k` skips verification; we only need
   it written to storage). The request goes through certmagic's
   `Config.saveCert` → `cfg.Storage.Store` (certmagic `config.go:1188`) → our
   override, which writes the file at `0644` and the parent dir at `0755`.
6. **Polls** the storage dir until a cert/key file appears (issuance is
   synchronous with the first request), with a timeout.
7. **Asserts** on the storage tree (see Assertion scope below).
8. **Cleans up** via `trap '...' EXIT` — kill the caddy process, remove the
   temp dir. Runs on success and failure alike.

### Why `tls internal` instead of mocking the cert

Caddy's `tls internal` issuer produces a real, self-signed certificate for
`localhost` on demand and persists it through the **same** certmagic storage
pipeline that ACME uses (`cfg.Storage.Store` at `config.go:1188`). This means:

- No ACME, no network, no Let's Encrypt rate limits, no Pebble container.
- The cert bytes are real and written to disk by our `Store()` override — the
  exact code path we care about.
- No mocking layer that could drift from certmagic's real behavior.

The cert is untrusted, but the test client uses `InsecureSkipVerify`; we never
validate the cert, we only need it persisted so we can check the file mode.

### Assertion scope (the subtle part)

certmagic writes **all** persisted values through `Storage.Store` — there is no
bypass path. Verified call sites in certmagic v0.23.0:

| File:line | What it stores |
|---|---|
| `config.go:1188` | cert + key bytes (the main path) |
| `maintain.go:605` | site metadata (cert resource JSON) |
| `maintain.go:721` | last-clean info |
| `maintain.go:929` | compromised key (renewal path) |
| `ocsp.go:129` | OCSP staple |
| `solvers.go:634` | DNS challenge tokens (ACME only — not hit by `tls internal`) |

Our `Store()` override is the sole writer for all of these, and it writes files
at `0644` and creates parent dirs at `0755`. So **all files written via `Store`
are `0644`**.

**Exception — lock files:** `FileStorage.Lock()` creates lock files via
`atomicallyCreateFile` (certmagic `filestorage.go:378`), which calls
`os.OpenFile(..., 0644)` for the file (good — 0644) but
`os.MkdirAll(filepath.Dir(filename), 0700)` for the `locks/` directory. This
does **not** go through our override, so `<root>/locks/` will be `0700`.

Therefore the assertion is:

- **Every regular file** under the storage root (excluding `locks/`) is mode `0644`.
- **Every directory** under the storage root (excluding `locks/`) is mode `0755`.
- The `locks/` directory and its contents are **excluded** from assertion
  (they are created by certmagic's `Lock()`, not our `Store()`).

This precisely targets the plugin's contract — the permissions of files it
writes — without false-positiving on certmagic's own lock-file plumbing.

### Caddyfile

```
{
  storage permissive_file_storage {
    root "{$STORAGE_ROOT}"
  }
  admin off
  http_port 8080
  https_port 9443
}

localhost {
  tls internal
  respond "ok" 200
}
```

Use fixed high ports (`http_port 8080` / `https_port 9443`) rather than `0`, so
the `curl` step knows exactly where to connect. These are free on GitHub CI
runners and almost always free locally; the script can probe and skip if not.
`admin off` disables the admin endpoint entirely, eliminating the
`localhost:2019` port-collision class of flakiness. `STORAGE_ROOT` is the temp
dir.

### CI integration

Add a `test` job to `.github/workflows/deploy.yml` that runs **before** the
docker build job and gates it:

```yaml
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
      - run: bash test/e2e.sh
```

The existing `deploy` job becomes `needs: [test]`. The script self-skips locally
if `xcaddy` is absent, but CI installs it. `setup-go` is still needed because
`xcaddy build` requires a Go toolchain.

### What this catches

1. **The exact bug we just fixed** — `Store` signature mismatch with
   `certmagic.Storage` → the `xcaddy build` step fails to compile → test fails.
2. **Permission regressions** — if `0644`/`0755` ever reverts to `0600`/`0700`,
   the assertion fails.
3. **Module wiring breakage** — if Caddy stops recognizing
   `caddy.storage.permissive_file_storage`, the binary won't start or won't
   route through our storage, and no cert file appears → test fails.

### Runtime

~30–60s in CI (xcaddy build dominates; ~20s for the build, a few seconds for
startup + issuance + assertion). Acceptable for a push-gated job.

## Resolved decisions

- **Caddy version pin in the test:** hardcode `v2.10.0` (matches go.mod) with
  a comment that it should be kept in sync with go.mod's caddy requirement.
  Determinism over auto-discovery.
- **Admin endpoint:** `admin off`. Avoids the `localhost:2019` port-collision
  class of flakiness entirely. Trade-off: no clean `caddy stop` via API, so
  cleanup relies on process kill — acceptable for a test.
- **Readiness vs. issuance:** these are separate phases. Readiness = process
  alive after grace period (config parsed OK). Issuance = poll storage dir for
  the cert file after making the HTTPS request. The storage dir being empty at
  startup is expected; it only fills after the first request.
- **Bash script, not Go test:** the test is a shell pipeline (build → run → curl
  → inspect FS); a Go `_test.go` would wrap `os/exec` around the same commands
  with no Go-level benefit. A committed `test/e2e.sh` runs identically in CI
  and locally (`bash test/e2e.sh`), and keeps the permission assertions
  readable (`find ... ! -perm`).
