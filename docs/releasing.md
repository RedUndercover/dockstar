# Release Guide

## Outputs

Dockstar releases target Linux amd64 and contain four binaries:

- `dockstar`
- `dockstar-ui`
- `dockstar-wake-helper`
- `dockstar-startup-helper`

GoReleaser produces:

- `tar.gz` archive.
- Arch `pkg.tar.zst` package.
- Debian `deb` package.
- RPM package.
- SHA-256 checksum manifest.
- Syft JSON SBOM for each archive and package.

Native packages require systemd 257 or newer because Dockstar relies on its
strict same-user `block` inhibitor semantics.

## Pre-release checklist

```sh
git status --short
make generate
git diff --exit-code
make test
make race
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest $(go list ./... | grep -v '/internal/protocol$')
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run github.com/goreleaser/goreleaser/v2@v2.17.0 check
```

Update `CHANGELOG.md`, verify documentation matches the current schema and CLI,
and ensure the repository is clean before tagging.

## Snapshot build

Install Syft where GoReleaser can find it, then run:

```sh
go install github.com/anchore/syft/cmd/syft@v1.48.0
PATH="$HOME/go/bin:$PATH" make package
```

Snapshot artifacts are written to `dist/` and are ignored by Git.

Inspect at least one native package and the portable archive:

```sh
bsdtar -tf dist/*.pkg.tar.zst
tar -tzf dist/*.tar.gz
sha256sum -c dist/checksums.txt
```

Package contents should include user and system service units, D-Bus
activation, Polkit policy, both desktop files, icon, manual page, completions,
license, and notices in addition to all binaries.

## Tagged release

The release workflow runs for tags matching `v*`:

```sh
git tag -s v0.1.0 -m 'Dockstar 0.1.0'
git push origin main v0.1.0
```

The GitHub workflow installs build dependencies, Cosign tooling, and Syft; runs
GoReleaser; uploads the release assets; and creates GitHub build-provenance
attestations for files in `dist/`.

The local repository does not require a remote for snapshot builds. Publishing
requires a GitHub remote at `redundercover/dockstar` and a workflow-provided
`GITHUB_TOKEN`.

## Package installation behavior

The package script leaves `dockstar.service` globally disabled so each user can
control it with **Start Dockstar automatically**. The KDE-only system autostart
entry makes the tray available; the toggle writes a per-user tray override and
changes the user-service enable state together. Opening the tray, settings, or
CLI may still D-Bus activate the daemon on demand.

The package enables `dockstar-wakeup.service`, but the unit runs only when
`/etc/dockstar/wake.json` exists. Package installation does not create that
policy, so hardware wake remains opt-in. On upgrades, starting the one-shot
service immediately reapplies an existing policy and repairs installations
where the service was previously left disabled.

The package also enables `dockstar-startup.service`. It needs a root-owned
`/var/lib/dockstar/startup.json` policy created through authenticated enrollment;
installation alone never approves displays. Use Settings → Displays or
`dockstar startup enable`, then `dockstar startup sync` after approval changes.

Actual package removal disables the boot service without deleting the saved
wake policy or baseline. Upgrade removal phases leave it enabled, and
source-only user installs do not install or manage the system service.

## Dependency updates

Dependabot tracks Go modules and GitHub Actions weekly. For a manual Go update:

```sh
go get MODULE@VERSION
go mod tidy
go mod verify
make test
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

For Wails, WebKitGTK, Vite, or Lucide updates, rebuild the embedded frontend and
visually inspect every settings view at the 1360x860 target size and 940x640
minimum size. For `wlcl` or KDE protocol updates, regenerate bindings and
retest mirror mode on the oldest supported Plasma version.

## Post-release verification

1. Download artifacts from the public release rather than using local files.
2. Verify `checksums.txt`.
3. Inspect the GitHub provenance attestation.
4. Install each package in a clean matching distribution.
5. Confirm daemon activation, tray autostart, config creation, and uninstallation.
6. Confirm wake remains unchanged before explicit setup.
7. Test extend, mirror, external-only, and internal-only with real hardware.
