# Dockstar mockup generation prompts

Mode: built-in image generation tool.

The ThemeTime mockups and current Dockstar icon were supplied as visual
references. Generated text is illustrative; the repository documentation and
Go types remain authoritative.

## Dock overview

```text
Use case: ui-mockup
Asset type: polished desktop Linux application screen, 16:10 landscape
Primary request: create the first screen in a cohesive Dockstar KDE Plasma desktop app redesign, based on the supplied ThemeTime UI references; this screen is "Dock Overview"
Input images: ThemeTime mockups are visual style and composition references only; the Dockstar monitor-with-star icon is the product identity reference
Subject: a full Dockstar app window showing current dock state at a glance, with a compact left navigation rail, a large graphical display topology as the central hero, a slim system-status inspector on the right, and quick layout actions below
Style/medium: realistic high-fidelity product UI screenshot, crisp contemporary KDE desktop design, shippable and practical, not concept art
Composition/framing: straight-on full app window, about 1600x1000 proportions; compact top bar; left navigation about 17%, central workspace 63%, right inspector 20%; no desktop wallpaper visible
Color palette: charcoal and graphite surfaces, cool blue selection and focus, restrained warm yellow star accent inherited from Dockstar icon, green only for healthy state, red only for errors; not monochromatic navy
Materials/textures: subtle frosted acrylic only in sidebar and top bar, opaque work surfaces, hairline separators, modest 6-8px corners, no glow
Text: Dockstar, Overview, Displays, Automation, Wake & power, Diagnostics, Docked, Extended desktop, Apply, Laptop display, Built-in · 2560 × 1600, LG UltraFine, DisplayPort-1 · 3840 × 2160, Primary, Layout, Extend, Mirror, External only, Internal only, System status, Daemon, Running, Display backend, KScreen, Lid, Open, Last updated, Just now
Constraints: the center topology must clearly show a smaller laptop display to the left of a larger external monitor, aligned as an extended desktop; primary badge on LG UltraFine; practical icon buttons; strong hierarchy; accessible contrast; selected Overview item; visible controls correspond to Dockstar capabilities; use the Dockstar icon accurately at small size
Avoid: generic analytics dashboard, timeline, charts, mobile layout, sci-fi HUD, excessive transparency, gradient orbs, nested cards, giant text, marketing copy, decorative illustration, macOS styling, watermark
```

## Displays workbench

```text
Use case: ui-mockup
Asset type: polished desktop Linux application screen, 16:10 landscape
Primary request: create the second screen in the same cohesive Dockstar KDE Plasma app redesign; this screen is "Displays"
Input images: preserve the Dock Overview visual system, Dockstar icon, and ThemeTime-inspired KDE navigation
Subject: a full Dockstar Displays screen for approving external displays and configuring the selected display, with a graphical two-monitor arrangement canvas, a compact connected-device list, and focused properties inspector
Style/medium: realistic high-fidelity product UI screenshot, crisp contemporary KDE desktop utility, immediately implementable
Composition/framing: straight-on full app window about 1600x1000; same top bar and left rail as Overview; selected Displays nav; top-half arrangement canvas, connected-display rows at lower left, selected-display settings at lower right
Color palette: same charcoal and graphite surfaces, cool blue selection, restrained yellow star accent, green approved state, neutral white text
Materials/textures: opaque content surfaces, subtle frosted sidebar/top bar, hairline borders, 6-8px radii, restrained shadows
Text: Dockstar, Overview, Displays, Automation, Wake & power, Diagnostics, Display arrangement, Identify, Laptop display, Built-in, LG UltraFine, DisplayPort-1, Primary, Connected displays, Approved, Built-in display, 2560 × 1600 · 120 Hz, External display, 3840 × 2160 · 60 Hz, Display settings, Use this display, Resolution, 3840 × 2160, Scale, 100%, Position, Right of laptop, Primary priority, 10, Device fingerprint, GSM-5B09-A1C83F, Save changes
Constraints: depict draggable rectangular screens labeled 1 and 2 with screen 2 larger and to the right; selected screen 2 outlined blue; built-in approval disabled; external Approved switch on; fingerprint visually secondary; standard controls; accessible contrast; legible typography
Avoid: generic dashboard, timeline, charts, mobile layout, sci-fi HUD, decorative graphics, giant cards, excessive blur, macOS styling, fake brightness or color calibration, marketing text, watermark
```

## Docking automation

```text
Use case: ui-mockup
Asset type: polished desktop Linux application screen, 16:10 landscape
Primary request: create the third screen in the same cohesive Dockstar KDE Plasma app redesign; this screen is "Automation"
Input images: preserve the generated Dockstar visual system, navigation, spacing, icon language, and product identity; ThemeTime references establish KDE-native quality
Subject: a full Dockstar Automation screen for docking detection and coordinated system behavior, with a focused policy form in the main workspace and a narrow live outcome preview on the right
Style/medium: realistic high-fidelity product UI screenshot, crisp contemporary KDE desktop utility, practical and implementation-ready
Composition/framing: straight-on full app window about 1600x1000; same compact top bar and left navigation; selected Automation nav; full-width grouped rows with separators; right inspector summarizes the next dock and undock
Color palette: same charcoal/graphite palette, cool blue selection/focus, warm yellow star accent used sparingly, green healthy state; good neutral contrast
Materials/textures: subtle frosted sidebar/top bar only, opaque content, hairline dividers, modest 6-8px corners
Text: Dockstar, Overview, Displays, Automation, Wake & power, Diagnostics, Docking automation, Detection, Approved displays only, Default layout, Extend, When lid is closed, External only, System coordination, Audio output, Prefer display audio, Network, Prefer wired, AC power profile, Performance, Restore previous audio and power settings on undock, Closed-lid undock, Lock session, Suspend computer, Next dock, LG UltraFine becomes primary, Extend desktop to the right, Switch audio to HDMI / DisplayPort, Prefer wired connection, Use Performance profile, Next undock, Restore previous system settings, Save changes
Constraints: select menus for option sets, switches or checkboxes for binary behavior, clear section headings, quiet descriptive labels, compact ordered outcome lists, small Ready state, controls corresponding to Dockstar configuration, accessible contrast, legible text, consistent Dockstar icon
Avoid: generic analytics dashboard, timeline, charts, mobile layout, sci-fi HUD, oversized or nested cards, excessive transparency, decorative artwork, macOS styling, fake scheduling, unsupported rules engine, wake controls on this screen, marketing copy, watermark
```
