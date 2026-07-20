# Configuration

Dockstar creates a fresh configuration on first start. Automatic detection
defaults to an empty allowlist, so connecting an unknown monitor emits a pending
display event but does not reconfigure outputs.

Layouts are `extend`, `mirror`, `external-only`, and `internal-only`. The
default is `extend`; a closed lid defaults to `external-only`. Mirror mode uses
the primary external display as its source and chooses the largest compatible
mode at no more than 60 Hz.

Display IDs use `edid:` followed by the SHA-256 EDID fingerprint. When EDID is
unavailable Dockstar falls back to the persistent KDE UUID, then a hashed
connector identifier. Rules may set a label, approval, primary rank, scale,
mode, relative position, and preferred audio sink.

Configuration writes are atomic, mode `0600`, and revisioned. A D-Bus update
with an old revision fails with `RevisionConflict`.
