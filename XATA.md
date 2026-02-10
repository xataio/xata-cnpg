# Xata Fork of CloudNativePG

This is Xata's fork of [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg).

## Development Workflow

### Making Changes

1. Make your code changes
2. If CRDs changed, regenerate them:
   ```bash
   make manifests
   cd charts && make update-crds
   ```
3. Test locally, commit, and open a PR

### CI Pipeline

**On every push:**
- Runs linters and tests
- Verifies CRDs are up to date (both `config/crd` and `charts/`)
- Builds and pushes image + Helm chart

**Artifacts:**

| Artifact | Location |
|----------|----------|
| Image | `ghcr.io/xataio/xata-cnpg:g<commit>` |
| Chart | `oci://ghcr.io/xataio/xata-cnpg/charts/xata-cnpg:0.0.0-g<commit>` |

### Versioning

Both image and chart use the same version: `g<7-char-commit>`.

This means:
- Every commit produces a unique, traceable version
- Chart's `appVersion` matches the image tag
- No "latest" tags - always explicit versions

## Helm Chart

See [charts/README.md](charts/README.md) for chart-specific documentation.
