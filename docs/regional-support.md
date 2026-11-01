# Regional download behavior

The current client does not detect mainland-China network location, select regional mirrors, or retry through alternate sources. It delegates package retrieval to an already configured package manager. There is no IP geolocation request or telemetry.

Do not treat locale, timezone, or language as proof of network region. Do not add a mirror based on a hostname alone. A regional source needs a verified operator and publisher-integrity metadata, and it must not silently replace the source selected in a reviewed plan. Until that source resolver is implemented, a user can configure their package manager independently and review the source label shown by DX.
