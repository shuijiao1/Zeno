# Third-party license review

Zeno itself is MIT licensed. The dependency inventory is reproducible from `go.mod`/`go.sum`, `web/package-lock.json`, and the SPDX SBOM attached to each published OCI platform image.

Review baseline:

- Go runtime dependency families are permissive (BSD/MIT/Apache-style): Gorilla WebSocket, `golang.org/x/*`, and modernc SQLite plus its transitive modules.
- Browser runtime dependencies (`react`, `react-dom`, `copy-to-clipboard`, `flag-icons`) are MIT licensed. The complete npm lock currently contains MIT, Apache-2.0, MPL-2.0, ISC, BSD-3-Clause, and 0BSD packages; MPL packages are build/development tooling, not a separately distributed server component.
- The Debian image retains package copyright/license records under `/usr/share/doc/*/copyright`. The release workflow publishes an SPDX image SBOM and build provenance for every platform.
- The Agent has its own inventory in the Zeno-Agent repository.

No dependency identified by this review changes Zeno's source license or requires bundling generated source. This inventory is a release check, not legal advice. When dependencies change, regenerate the SBOM and re-check module/package license metadata rather than hand-copying a stale, oversized license dump.
