# Concepts

## Output

An output is a normalized KScreen display record. It contains a connector,
Dockstar device ID, panel flag, approval state, geometry, scale, priority, and
advertised modes.

## Device identity

Dockstar prefers a SHA-256 hash of the sysfs EDID. It falls back to a KDE UUID,
then a hash of the connector name. Config rules use this ID, not KScreen's
numeric output ID.

## Approved display

Internal panels are always considered approved. External approval depends on
the detection policy:

- `allowlist`: a matching rule must have `allowed: true`.
- `all`: every external is approved.
- `off`: no external is approved for automatic docking.

## Docked

Docked means detection is active and at least one approved external display is
connected. It is a Dockstar state, not simply "some cable exists."

## Layout

A layout is an output transaction:

- `extend`: panel and approved externals, selected external primary.
- `mirror`: panel replicates one selected external at a common mode.
- `external-only`: approved externals on, panel off.
- `internal-only`: externals off, panel on.

The lid chooses between the normal and closed-lid layout.

## Primary

The external display with the highest `primaryRank` wins. A rank tie is broken
by largest preferred resolution.

## Pending display

A connected non-panel output that is not approved is pending. The daemon emits
a D-Bus signal so clients can offer approval, but it does not apply a layout.

## Forced evaluation

Normal polling skips identical device, approval, and lid signatures. Apply,
resume, and selected config operations clear or bypass that cache.

## Wake policy

Wake policy is root-owned system state. It controls eligible PCI display and
USB controller `power/wakeup` files. It is independent from the user display
allowlist because the computer must wake before EDID can be read.

## Sleep protection

Sleep protection is unprivileged, temporary daemon state. It prevents
automatic sleep while an approved dock is active, during post-resume discovery,
and briefly during a closed-lid disconnect. One persistent logind `sleep`
block lease is the authoritative backend. Because that lease also blocks an
ordinary Plasma Sleep action, Dockstar provides a generation-scoped
`SuspendNow` path that releases it for one suspend cycle. Sleep protection is
independent of the root-owned hardware wake policy and never rewrites the
desktop's saved power settings.

## Restore baseline

The wake helper records controller state once before its first change. Restore
returns to that mixed original state. Dock actions have a different,
memory-only baseline for audio, power, Ethernet, and Wi-Fi.
