# Terminal workspace

The interactive installer uses Bubble Tea v2, Bubbles v2, and Lip Gloss v2. It opens on a dashboard rather than a command prompt. The workspace keeps navigation, the current task, and contextual details visible together. Search, filters, lists, dialogs, and actions are interactive with either the mouse or keyboard.

## Layout and navigation

- At 120 columns and 32 rows or larger, the app shows navigation, the task workspace, and an inspector.
- At 80–119 columns, it shows navigation and the task workspace; details remain available through the content and scrolling.
- Smaller terminals use one scrollable pane. Click **Sections** or press `Ctrl+P` to open the command palette and choose any section.

The persistent sections are Home, Tools, Profiles, Environment, Plan, Activity, and Results. Source configuration is under **Advanced**. Resizing recalculates layout and click targets.

## Mouse and keyboard

Click visible navigation items, rows, filters, and actions. Hovered and keyboard-focused controls are highlighted. The mouse wheel scrolls the pane under the pointer, including the inspector and long dialogs.

Use `Tab` / `Shift+Tab` to move focus, arrow keys to move through selectable rows, `Space` to select, and `Enter` to activate. `Page Up` / `Page Down` scroll long content. Press `Ctrl+P` for commands, `?` for Help, `Esc` to close a dialog, and `q` to quit. Help also lists the controls available in the current interface. All sections and actions remain reachable by keyboard when a terminal does not send mouse events.

## Tools and profiles

Search the catalog, filter by category or installation state, and select individual tools or profile cards. Tool details show platform support, dependencies, detected status, version channel, source, and the available installation method. Profiles are editable starting points; applying them returns to Tools so selections can be changed.

The Environment table shows what is already installed. Search matches names, paths, versions, package ownership, and diagnostics. The inspector shows executable paths, versions, package records, and inventory warnings.

## Review and install

Build or refresh a plan from the Plan section. Review install, preserve, and blocked summaries, then inspect each item for source, version, package ID, digest, download estimate, destination, setup actions, PATH changes, command preview, and package-manager disclosures. Nothing is installed during planning.

The final approval dialog refreshes inventory before starting. If the current plan changes or becomes blocked, DX returns to review and requires approval again. Package-manager prompts remain visible. Existing tools are preserved and are not upgraded.

Advanced Sources retains package-manager preference, source policies, custom and offline archives, registry overrides, and signed bundles. Results provide per-tool verification and a redacted diagnostic report action.

## Fallback and coverage

If Bubble Tea cannot render, the CLI offers a line-oriented selection and approval flow. A non-TTY install invocation exits before changes. `catalog`, `inventory`, and `plan` remain available without a TTY. Progress reports completed operations and the current task; it does not invent byte percentages.

Automated coverage includes fresh-inventory approval, plan invalidation and blockers, line fallback, mouse navigation and selection, search and filters, advanced settings, dialog input, focus traversal, scrolling, five responsive terminal sizes, Unicode display width, ASCII fallback, and golden snapshots. Set `DX_ASCII=1` to force ASCII rendering, or `DX_ASCII=0` to force Unicode rendering. Mouse input requires a terminal emulator that sends mouse events; keyboard access works without it.
