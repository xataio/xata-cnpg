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
3. Test locally:
   - **Unit tests**: `make test`
   - **Local cluster**: `./hack/setup-cluster.sh create load deploy` (builds and deploys to a local `kind` cluster)
   - **E2E tests**: See [contribute/e2e_testing_environment/README.md](contribute/e2e_testing_environment/README.md)

   For full development environment setup, see [contribute/development_environment/README.md](contribute/development_environment/README.md).
4. Commit and open a PR

### CI Pipeline

**On every push:**
- Runs linters and tests
- Verifies CRDs are up to date (both `config/crd` and `charts/`)
- Builds and pushes image + Helm chart

**Artifacts:**

| Artifact | Location |
|----------|----------|
| Image | `ghcr.io/xataio/xata-cnpg/cloudnative-pg:g<commit>` |
| Chart | `oci://ghcr.io/xataio/xata-cnpg/charts/cloudnative-pg:0.0.0-g<commit>` |
| Manifest | `oci://ghcr.io/xataio/xata-cnpg/cloudnative-pg-manifest:g<commit>` |

> **Note:** The operator manifest is generated and pushed as an OCI artifact but is not currently used. It's available for future use cases like GitOps deployments without Helm.

### Versioning

- **Image tag**: `g<7-char-commit>` (e.g., `gf6db633`)
- **Chart version**: `0.0.0-g<7-char-commit>` (e.g., `0.0.0-gf6db633`)

This means:
- Every commit produces a unique, traceable version
- Chart's `appVersion` matches the image tag
- No "latest" tags - always explicit versions

## Helm Chart

See [charts/README.md](charts/README.md) for chart-specific documentation.
