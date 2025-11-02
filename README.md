# Render-Diff
render-diff is intended to render Helm charts or Kustomizations stored in a git repository and compare the current local reference vs a target git reference (defaults to main).

# Setup
### Requirements
* `make`
* `git`
* Go `1.24` or newer

# Installation

`go install github.com/dlactin/render-diff@latest`

# Flags

* `-path` - Relative path to the Chart or Kustomization (required).
* `-ref` - Target Git ref to compare against (e.g., 'test', 'develop') (default "main").
* `-values` - Path to an additional values file, relative to the chart-path (can be specified multiple times). The chart's `values.yaml` is always included first. Only used for Chart path.

# Examples

### This should be run while your current directory is within your git repository

#### Checking a Helm Chart diff against another target ref
* ```render-diff -path=./examples/helm/helloWorld -values=values-dev.yaml --ref development```
#### Checking Kustomize diff against the main branch
* ```render-diff -path=./examples/kustomize/helloWorld```

# TODO
* add `-source-ref`, we could provide a source ref instead of using the current git ref