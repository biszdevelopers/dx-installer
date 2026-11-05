# Regional download behavior

DX uses local operating-system region, locale, and time-zone settings as best-effort signals. On Windows it reads the current user's locale; on all platforms it can read `LC_ALL`, `LC_MESSAGES`, and `LANG`, plus the local time zone. It does not use IP geolocation, contact a location service, or send telemetry. These settings describe user preferences and are not proof of network location.

The Sources screen and `--source-policy` flag provide explicit choices:

| Policy | Behavior |
| --- | --- |
| `automatic` | Use a regional mirror after retryable official-source failures when local signals consistently indicate mainland China; conflicting signals require a choice. |
| `official-only` | Use only the official artifact URL. |
| `official-first` | Try the official URL first, then an eligible catalog-pinned mirror after retryable failures. |
| `mainland-china` | Same download order as `official-first`, selected explicitly. |
| `global` | Do not use regional mirrors. |

The default is `automatic`. Choose a policy in the Sources screen or pass it to `plan` or `install`, for example `--source-policy official-only`. Source policy does not change package-manager repository settings.

Regional URLs are declared per artifact in the embedded catalog. A mirror is eligible only when it is bound to the same catalog-pinned SHA-256 digest as the official artifact. The current catalog declares HUST mirrors for every Go archive it supports: Windows amd64/arm64, macOS amd64/arm64, Linux amd64/arm64, and FreeBSD amd64. The mirror lists these Go release files for all four operating systems, and DX verifies downloaded bytes against the embedded digest before extraction.

Regional coverage is partial across the full tool catalog. Android SDK Command-Line Tools and Zephyr SDK currently have no independently verified regional mirror entry, so those artifacts use their official URLs only. DX does not guess a mirror URL or use a generic proxy for them.

Retries are bounded. DX retries transient network and server failures with a maximum of three attempts per source, a timeout, and bounded backoff. It does not fall back after an integrity mismatch or an unsafe redirect. Package managers continue to use their own configured repositories, which DX does not independently verify.
