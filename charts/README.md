# xata-cnpg Helm Chart

## Makefile Targets

| Target | Description |
|--------|-------------|
| `update-crds` | Sync CRDs from `config/helm` into the chart |
| `inject-tags` | Set image repo/tag in values.yaml |
| `push-charts` | Version and push chart to OCI registry |
| `lint` | Validate chart |
| `template` | Render chart templates |

## Local Testing

```bash
make inject-tags IMAGE=ghcr.io/xataio/xata-cnpg TAG=local
make lint
make template
helm install xata-cnpg ./xata-cnpg -n cnpg-system --create-namespace
```

## Registry

Charts are pushed to: `oci://ghcr.io/xataio/xata-cnpg/charts`
