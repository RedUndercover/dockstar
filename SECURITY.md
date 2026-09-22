# Security Policy

## Reporting

Report security issues privately through GitHub's security advisory feature at
`github.com/redundercover/dockstar/security/advisories/new`. Do not open a
public issue for an undisclosed vulnerability.

## Privileged boundary

`dockstar-wake-helper` accepts four fixed operations, discovers eligible PCI
controllers from sysfs, validates all device identifiers, and refuses arbitrary
filesystem paths or wake states.

`dockstar-startup-helper` accepts only enable/sync/disable operations.
Administrator authentication authorizes a bounded, validated configuration
input; only explicitly approved EDID fingerprints are persisted to a fixed
root-owned policy path. It never accepts an arbitrary privileged command or
destination. Approval changes require authenticated synchronization, including
revocations. Synchronizing the final revocation writes a disabled policy.

The hardened `dockstar-startup.service` also runs as root. It only reads DRM,
the boot policy, and logind session/inhibitor state. At graphical login it waits
at most 15 seconds for a Dockstar sleep-block lease owned by a local graphical
session's UID, excluding its own process, before releasing its lease. It never
rearms within that overlapping login session after handoff. It does not access
home directories or control a compositor.
EDID identifies a display; it is not a cryptographic device-authentication
mechanism and can be spoofed by hardware.

The session daemon, ordinary CLI commands, tray, and settings window run as the
desktop user.

Wake configuration is opt-in and the original state of every changed source is
recorded before the first change.
