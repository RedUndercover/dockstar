# Security Policy

## Reporting

Report security issues privately through GitHub's security advisory feature at
`github.com/redundercover/dockstar/security/advisories/new`. Do not open a
public issue for an undisclosed vulnerability.

## Privileged boundary

Only `dockstar-wake-helper` runs with administrator privileges. It accepts four
fixed operations, discovers eligible PCI controllers from sysfs, validates all
device identifiers, and refuses arbitrary filesystem paths or wake states.
The daemon, CLI, tray, and settings window run entirely as the desktop user.

Wake configuration is opt-in and the original state of every changed source is
recorded before the first change.
