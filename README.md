# rdv (render-diff-validate)
`rdv` provides a fast and local preview of your rendered Kubernetes manifest changes.

It renders your local Helm chart or Kustomize overlay, validates rendered manifests via kubeconform and then compares the resulting manifests against the version in a target git ref (like 'main' or 'develop').

It prints a colored diff of the final rendered YAML.

## Requirements
* `make`
* `git`
* Go `1.24` or newer

## Installation

You can install `rdv` directly using `go install`:

```sh
go install github.com/dlactin/rdv@latest
```

# Flags

| Flag | Shorthand | Description | Default |
| :--- | :--- | :--- | :--- |
| `--path` | `-p` | Relative path to the chart or kustomization directory. | `.` |
| `--ref` | `-r` | Target Git ref to compare against. Will try to find its remote-tracking branch (e.g., origin/main). | `main` |
| `--values` | `-f` | Path to an additional values file (can be specified multiple times). | `[]` |
| `--update` | `-u` | Update helm chart dependencies. Required if lockfile does not match dependencies | `false` |
| `--semantic` | `-s` |  Enable semantic diffing of k8s manifests (using dyff) | `false` |
| `--debug` | `-d` | Enable verbose logging for debugging | `false` |
| `--validate` | `-v` | Validate rendered manifests with kubeconform | `false` |
| `--output` | `-o` | Write the local and target rendered manifests to a specific file path | `false` |
| `--version` | | Prints the application version. | |
| `--help` | `-h` | Show help information. | |

# Examples

### This must be run while your current directory is within your git repository

#### Checking a Helm Chart diff against another target ref
* ```rdv -p ./examples/helm/helloworld -f values-dev.yaml -r development```
#### Checking a Helm Chart diff and validating our rendered manifests
* ```rdv -p ./examples/helm/helloworld --validate```
#### Checking Kustomize diff against the default (`main`) branch
* ```rdv -p ./examples/kustomize/helloworld```
#### Checking Kustomize diff against a tag
* ```rdv -p ./examples/kustomize/helloworld -r tags/v0.5.1```

