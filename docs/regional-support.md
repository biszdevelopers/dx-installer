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

Regional URLs are declared per artifact in the embedded catalog. A mirror is eligible only when it is bound to the same catalog-pinned SHA-256 digest as the official artifact. The current catalog declares regional mirrors for Go on Linux amd64 and arm64; other artifacts continue to use their official sources. DX verifies the downloaded bytes against the pinned digest regardless of which URL supplied them.

Retries are bounded. DX retries transient network and server failures with a maximum of three attempts per source, a timeout, and bounded backoff. It does not fall back after an integrity mismatch or an unsafe redirect. Package managers continue to use their own configured repositories, which DX does not independently verify.
