SHELL := /bin/bash

CHART_NAME := xata-cnpg
CHART_REGISTRY ?= oci://ghcr.io/xataio/xata-cnpg/charts

GIT_COMMIT_FULL := $(shell git rev-parse HEAD)
GIT_COMMIT_SHORT := g$(shell printf '%.7s' $(GIT_COMMIT_FULL))

env: # Print Makefile variables for debugging
	@echo "CHART_NAME: $(CHART_NAME)"
	@echo "CHART_REGISTRY: $(CHART_REGISTRY)"
	@echo "GIT_COMMIT_FULL: $(GIT_COMMIT_FULL)"
	@echo "GIT_COMMIT_SHORT: $(GIT_COMMIT_SHORT)"

# Update CRDs from operator source
update-crds:
	@echo "Updating CRDs from config/helm"
	kustomize build ../config/helm > $(CHART_NAME)/templates/crds/crds.yaml

# Inject image tag into chart values.yaml
# Usage: make inject-tags IMAGE=ghcr.io/xataio/xata-cnpg TAG=g1234567
inject-tags:
	@echo "Injecting image $(IMAGE):$(TAG) into $(CHART_NAME)/values.yaml"
	yq eval '.image.repository = "$(IMAGE)"' -i $(CHART_NAME)/values.yaml
	yq eval '.image.tag = "$(TAG)"' -i $(CHART_NAME)/values.yaml

# Version the chart with git commit
version-chart:
	@echo "Setting $(CHART_NAME) version to 0.0.0-$(GIT_COMMIT_SHORT)"
	yq eval '.version = "0.0.0-$(GIT_COMMIT_SHORT)" | .appVersion = "$(GIT_COMMIT_SHORT)"' -i $(CHART_NAME)/Chart.yaml

# Package and push chart
push-chart: version-chart
	@set -e; \
	echo "Packaging $(CHART_NAME)"; \
	helm package $(CHART_NAME); \
	echo "Pushing $(CHART_NAME) to $(CHART_REGISTRY)"; \
	helm push $(CHART_NAME)-0.0.0-$(GIT_COMMIT_SHORT).tgz $(CHART_REGISTRY); \
	rm -f $(CHART_NAME)-0.0.0-$(GIT_COMMIT_SHORT).tgz

# Push chart (run `make update-crds` locally if CRDs changed)
push-charts: push-chart

lint:
	helm lint $(CHART_NAME)

template:
	helm template $(CHART_NAME) $(CHART_NAME)
