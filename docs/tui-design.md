# Terminal interface

The application uses Bubble Tea v2 for the event loop and Lip Gloss v2 for restrained styling. The view is rendered from model state; installation commands run through an explicit process execution step, and background inventory checks return typed messages. The UI serializes package-manager operations.

## Screens

1. Overview — OS, architecture, Linux distribution, shell, package managers, detected tool state, and current selection.
2. Catalog — text search, category and inventory-state filters, multi-selection, dependencies, support, and the proposed package-manager method.
3. Profiles — combine predefined profiles and edit the resulting selection in Catalog.
4. Environment — search and inspect detected executables, versions, paths, package records, and diagnostics independently of the catalog query.
5. Plan — display preservation, installs, package identifiers, source labels, scopes, command preview, and blockers.
6. Sources — prefer a detected package manager or clear the preference.
7. Progress — show completed results and the current operation.
8. Results — show verification states and per-tool diagnostics.

Keyboard navigation is sufficient: Tab or 1–8 changes screens, arrows or `j`/`k` move through rows, Space selects, `f` searches the catalog or existing environment, `a` clears the active search, `[` and `]` change category, `s` changes state filter, `p` builds a plan, `i` begins approval, `?` shows help, and `q` exits. Environment search matches tool identity and state, executable paths and versions, diagnostics, and package records. Mouse input is not required.

## Approval and fallback

The user sees the plan before any package-manager process starts. Pressing `y` triggers a fresh inventory scan; a changed or blocked plan is shown again and is not executed until separately approved. If the interactive renderer returns an error, the CLI offers a line-oriented selection and approval flow. A non-TTY `install` invocation exits before changes. `catalog`, `inventory`, and `plan` remain available without a TTY.

## Known gaps

The screens include an Environment golden snapshot, a narrow-terminal resize bound, and a fuzz target for independent environment-search input. Broader snapshots and resize/fuzz coverage for every screen remain to be added. Source-policy radio controls, custom sources, offline archives, download progress, mouse support, and terminal capability detection beyond Bubble Tea are also not implemented. No progress percentages are fabricated; current tasks are shown at operation granularity.
