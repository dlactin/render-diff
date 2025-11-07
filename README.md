# Render-Diff
`render-diff` provides a fast and local preview of your rendered Kubernetes manifest changes.

It renders your local Helm chart or Kustomize overlay to compare the resulting manifests against the version in a target git ref (like 'main' or 'develop').
It prints a colored diff of the final rendered YAML.

## Requirements
* `make`
* `git`
* Go `1.24` or newer

## Installation

You can install `render-diff` directly using `go install`:

```sh
go install github.com/dlactin/render-diff@latest
```

# Flags

| Flag | Shorthand | Description | Default |
| :--- | :--- | :--- | :--- |
| `--path` | `-p` | Relative path to the chart or kustomization directory. | `.` |
| `--ref` | `-r` | Target Git ref to compare against. Will try to find its remote-tracking branch (e.g., origin/main). | `main` |
| `--values` | `-f` | Path to an additional values file (can be specified multiple times). | `[]` |
| `--debug` | `-d` | Enable verbose logging for debugging | `false` |
| `--version` | | Prints the application version. | |
| `--help` | `-h` | Show help information. | |

# Examples

### This must be run while your current directory is within your git repository

#### Checking a Helm Chart diff against another target ref
* ```render-diff -p ./examples/helm/helloworld -f values-dev.yaml -r development```
#### Checking Kustomize diff against the default (`main`) branch
* ```render-diff -p ./examples/kustomize/helloworld```
#### Checking Kustomize diff against a tag
* ```render-diff -p ./examples/kustomize/helloworld -r tags/v0.5.1```
