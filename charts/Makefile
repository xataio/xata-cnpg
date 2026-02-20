SHELL := /bin/bash

CHARTS ?= $(patsubst %/Chart.yaml,%,$(wildcard */Chart.yaml))
CHART_REGISTRY ?= oci://ghcr.io/xataio/xata-cnpg/charts

GIT_COMMIT_FULL := $(shell git rev-parse HEAD)
GIT_COMMIT_SHORT := $(shell printf '%.7s' $(GIT_COMMIT_FULL))
CHART_VERSION := 0.0.0-g$(GIT_COMMIT_SHORT)

env: # Print Makefile variables for debugging
	@echo "CHARTS: $(CHARTS)"
	@echo "CHART_REGISTRY: $(CHART_REGISTRY)"
	@echo "GIT_COMMIT_FULL: $(GIT_COMMIT_FULL)"
	@echo "GIT_COMMIT_SHORT: $(GIT_COMMIT_SHORT)"

version: # Print the chart version for the current commit
	@echo "$(CHART_VERSION)"

git-commit-sha: # Print the short git commit SHA
	@echo "$(GIT_COMMIT_SHORT)"

registry: # Print the chart registry/repo
	@echo "$(CHART_REGISTRY)"

# Update CRDs from operator source.
# Wraps output with Helm conditional so users can skip CRD installation via values.yaml (crds.create: false).
# This matches upstream's release process: https://github.com/cloudnative-pg/charts/blob/main/RELEASE.md
update-crds:
	@echo "Updating CRDs from config/helm"
	@echo '{{- if .Values.crds.create }}' > cloudnative-pg/templates/crds/crds.yaml
	@kustomize build ../config/helm >> cloudnative-pg/templates/crds/crds.yaml
	@echo '{{- end }}' >> cloudnative-pg/templates/crds/crds.yaml

# Inject tag for image into chart values.yaml files
# Usage: make inject-tags IMAGE=ghcr.io/xataio/xata-cnpg/cloudnative-pg TAG=g1234567
# Note: Silently skips if chart directory doesn't exist or has no image section
inject-tags:
	@SERVICE=$$(basename "$(IMAGE)"); \
	if [ -d "$$SERVICE" ]; then \
		if yq eval 'has("image")' "$$SERVICE/values.yaml" 2>/dev/null | grep -q true; then \
			echo "Injecting image and tag for $$SERVICE chart"; \
			yq eval '.image.repository = "$(IMAGE)"' -i "$$SERVICE/values.yaml"; \
			yq eval '.image.tag = "$(TAG)"' -i "$$SERVICE/values.yaml"; \
		fi; \
	fi

# Version a chart with git commit
# Usage: make version-chart SERVICE=cloudnative-pg
version-chart:
	@echo "Setting $(SERVICE) version to $(CHART_VERSION), appVersion $(GIT_COMMIT_SHORT)"
	@yq eval '.version = "$(CHART_VERSION)" | .appVersion = "$(GIT_COMMIT_SHORT)"' -i $(SERVICE)/Chart.yaml

# Package and push a single chart
# Usage: make push-chart SERVICE=cloudnative-pg
push-chart: version-chart
	@set -e; \
	echo "Packaging $(SERVICE)"; \
	helm package $(SERVICE); \
	echo "Pushing $(SERVICE) to $(CHART_REGISTRY)"; \
	helm push $(SERVICE)-$(CHART_VERSION).tgz $(CHART_REGISTRY); \
	rm -f $(SERVICE)-$(CHART_VERSION).tgz

# Push all charts (discovered via glob)
push-charts:
	@set -e; \
	for chart in $(CHARTS); do \
		$(MAKE) push-chart SERVICE=$$chart; \
	done

lint: lint-helm

lint-helm:
	@set -e; \
	for chart in $(CHARTS); do \
		echo "Linting $$chart"; \
		helm lint $$chart; \
	done

template:
	@set -e; \
	for chart in $(CHARTS); do \
		echo "Render chart template for $$chart..." >&2; \
		helm template $$chart $$chart; \
	done
