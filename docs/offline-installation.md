# Offline operation

The client does not currently install from local archives or generate/preload offline bundles. The existing package-manager adapters may work with repositories that the user has already configured for offline or LAN use, but DX does not identify that state or guarantee it.

Do not place an unverified archive in the temporary bootstrap workspace and assume it is trusted. A future bundle format must bind tool IDs, versions, platforms, architectures, artifacts, and hashes in signed metadata; reject path traversal and unsafe links; verify publisher keys; and keep user-supplied bundle storage outside the temporary DX workspace. Bundle creation and use are not part of the current supported interface.
