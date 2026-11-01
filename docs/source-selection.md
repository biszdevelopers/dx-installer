# Package-manager and source selection

The Sources screen lists package managers already discovered on the machine. Select one to prefer it when a selected tool declares a compatible installer for it. Press `0` to clear that preference. The plan shows the selected manager, package identifier, manifest source label, and exact argument array rendered for review. For scripted plan previews and explicitly approved line-mode installs, `--manager <name>` applies the same preference; the command fails if that manager is not detected.

No manager is installed, no repository is added, and no package-manager configuration is changed. A detected manager without a matching manifest entry is not used. When the preferred manager cannot install a tool, planning may use another already-detected manager declared by that tool.

## Current limits

`Source` in a manifest is descriptive metadata for an existing package manager configuration. The application does not verify that the configured repository is an official publisher, apply an official-only setting, ask for per-tool source overrides, configure registries, or implement retries and regional fallback. It does not download tools directly. Package managers may access their configured repositories and maintain their own caches.

Custom URLs and regional mirrors are intentionally not inferred from network location or fabricated in manifests. A future source resolver must require HTTPS, bounded redirects/timeouts, publisher-pinned integrity metadata, and explicit user approval before a non-default source is used. Until then, selecting a manager does not make its configured source an independently verified mirror.
