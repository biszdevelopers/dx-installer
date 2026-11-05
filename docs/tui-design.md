# Terminal interface

The application uses Bubble Tea v2 for its event loop and Lip Gloss v2 for restrained styling. The view is rendered from model state; package-manager and artifact tasks run through explicit process steps, and background inventory checks return typed messages. Package-manager operations are serialized.

## Screens

1. Overview — operating system, architecture, Linux distribution, shell, detected package managers, inventory state, and current selection.
2. Catalog — text search, category and inventory-state filters, multi-selection, dependencies, platform support, and the proposed installation method.
3. Profiles — combine predefined profiles and edit the resulting selection in Catalog.
4. Environment — inspect detected executables, versions, paths, package records, and diagnostics.
5. Plan — review preserved tools, installs, sources, package IDs, versions, archive digests and size estimates, install paths, setup actions, PATH changes, exact commands, and blockers.
6. Sources — select a preferred detected manager, choose a source policy, configure a per-tool custom URL or local archive, set an ecosystem registry, or load a signed offline bundle.
7. Progress — show completed results and the current operation.
8. Results — show verification state and per-tool diagnostics.

Keyboard navigation is sufficient: Tab or 1–8 changes screens, arrows or `j`/`k` move through rows, Space selects, `f` searches, `a` clears the active search, `[` and `]` change category, `s` changes the inventory-state filter, `p` builds a plan, `i` begins approval, `?` shows help, and `q` exits. Environment search covers tool identity and state, executable paths and versions, diagnostics, and package records.

On Sources, `a`, `c`, `g`, `o`, and `m` choose automatic, mainland-China, global, official-only, and official-first policies. `u` enters a catalog-pinned custom HTTPS archive URL, `v` sets a registry URL, `l` adds a catalog-pinned local archive, `b` loads a signed offline bundle, and `x` clears source overrides. Space or Enter selects a preferred detected package manager; `0` clears that preference. Every source or selection change invalidates the previous plan.

## Approval and fallback

The complete plan is shown before any package-manager or archive installation starts. Pressing `y` begins a fresh inventory scan; if the plan changes or becomes blocked, it is shown again and is not executed until separately approved. Artifact size disclosures, integrity hashes, vendor setup actions, and persistent PATH changes are visible before approval. Package-manager prompts remain visible to the user.

If Bubble Tea cannot render, the CLI offers a line-oriented selection and approval flow. A non-TTY install invocation exits before changes. `catalog`, `inventory`, and `plan` remain available without a TTY. Progress reports completed operations and the current task; it does not invent byte percentages.

## Automated coverage and terminal fallback

Tests cover source configuration and plan invalidation, approval refresh, fallback interaction, golden snapshots for all eight screens, resize bounds for every screen, Unicode display width, ASCII fallback, and fuzzed environment-search input. The renderer responds to `DX_ASCII=1`, `TERM=dumb`, ASCII locales, and legacy Windows console output code pages. Set `DX_ASCII=0` to force Unicode rendering. Mouse input is optional and is not required.
