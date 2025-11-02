package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dlactin/render-diff/internal/helm"
	"github.com/dlactin/render-diff/internal/kustomize"
	"github.com/hexops/gotextdiff" // This is archived, but I could not find a better alternative at the moment
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// ANSI codes for diff colors
const (
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
	colorReset = "\033[0m"
)

// valuesArray is a custom type to support multiple --values flags
type valuesArray []string

func (i *valuesArray) String() string {
	return strings.Join(*i, ", ")
}

func (i *valuesArray) Set(value string) error {
	*i = append(*i, value)
	return nil
}

// getRepoRoot finds the top-level directory of the current git repository.
func getRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to find git repo root: %w. Make sure you are running this inside a git repository. Output: %s", err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func main() {
	// Define and Parse Flags
	var valuesFlag valuesArray
	renderPathFlag := flag.String("path", "", "Relative path to the chart or kustomization directory (required)")
	gitRefFlag := flag.String("ref", "main", "Target Git ref to compare against (e.g., 'main', 'develop', 'v1.2.0')")

	flag.Var(&valuesFlag, "values", "Path to an additional values file, relative to the path (can be specified multiple times). The chart's 'values.yaml' is always included first.")

	flag.Parse()

	// Render Path is required
	if *renderPathFlag == "" {
		log.Println("Error: --path flag is required.")
		flag.Usage()
		os.Exit(1)
	}

	// A local git installation is required
	_, err := exec.LookPath("git")
	if err != nil {
		log.Fatal("git not found in PATH")
	}

	if out, err := exec.Command("git", "rev-parse", "--verify", "--quiet", *gitRefFlag).CombinedOutput(); err != nil {
		log.Fatalf("Invalid --ref %q: %s", *gitRefFlag, strings.TrimSpace(string(out)))
	}

	log.Printf("Starting diff against git ref '%s'", *gitRefFlag)

	// Get Git Root and Define Paths
	repoRoot, err := getRepoRoot()
	if err != nil {
		log.Fatal(err.Error())
	}

	// Get the absolute path from the path flag
	absPath, err := filepath.Abs(*renderPathFlag)
	if err != nil {
		log.Fatalf("Failed to resolve absolute path for -path %v", err)
	}

	// Get the relative path compared to the repoRoot)
	relativePath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		log.Fatalf("Failed to resolve relative path for -path %v", err)
	}

	if strings.HasPrefix(relativePath, "..") {
		log.Fatalf("Error: The provided path '%s' (resolves to '%s') is outside the git repository root '%s'.", *renderPathFlag, absPath, repoRoot)
	}

	localPath := filepath.Join(repoRoot, relativePath)

	// Resolve relative values file paths to absolute paths for the local render
	// This means we only support values files located in the path provided
	localValuesPaths := make([]string, len(valuesFlag))
	for i, v := range valuesFlag {
		localValuesPaths[i] = filepath.Join(localPath, v)
	}

	// Render Local (Feature Branch) Chart or Kustomization
	localRender := renderManifests(localPath, localValuesPaths)

	// Set up Git Worktree for Target Ref
	tempDir, err := os.MkdirTemp("", "diff-ref-")
	if err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}

	// Defer LIFO: 1. Remove dir (runs 2nd), 2. Remove worktree (runs 1st)
	// Clean up temp directories before our diff is returned
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			fmt.Printf("Error removing temporary directory %s: %v\n", tempDir, err)
		}
	}()

	defer func() {
		// Using --force to avoid errors if dir is already partially cleaned
		cleanupCmd := exec.Command("git", "worktree", "remove", "--force", tempDir)
		cleanupCmd.Dir = repoRoot // Run from the repo root
		if output, err := cleanupCmd.CombinedOutput(); err != nil {
			// Log as a warning, not fatal, so we don't stop execution
			log.Printf("Warning: failed to run 'git worktree remove'. Manual cleanup may be required. Error: %v, Output: %s", err, string(output))
		}
	}()

	// Create the worktree
	// Using -d to allow checking out a branch that is already checked out (like 'main')
	addCmd := exec.Command("git", "worktree", "add", "-d", tempDir, *gitRefFlag)
	addCmd.Dir = repoRoot // Run from the repo root
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Fatalf("Failed to create worktree for '%s': %v\nOutput: %s", *gitRefFlag, err, string(output))
	}

	targetPath := filepath.Join(tempDir, relativePath)

	// Resolve values file paths for the worktree
	targetValuesPaths := make([]string, len(valuesFlag))
	for i, v := range valuesFlag {
		targetValuesPaths[i] = filepath.Join(targetPath, v)
	}

	// Render Target Ref Chart or Kustomization
	targetRender := renderManifests(targetPath, targetValuesPaths)

	// Generate and Print Diff
	diff := createDiff(targetRender, localRender, fmt.Sprintf("%s/%s", *gitRefFlag, relativePath), fmt.Sprintf("local/%s", relativePath))

	if diff == "" {
		fmt.Println("\nNo differences found between rendered manifests.")
	} else {
		fmt.Printf("\n--- Manifest Differences (%s vs. Local) ---\n", *gitRefFlag)
		fmt.Println(colorizeDiff(diff))
	}
}

// renderManifests will render a Helm Chart or build a Kustomization
// and return the rendered manifests as a string
func renderManifests(path string, values []string) string {
	var renderedManifests string
	var err error

	if helm.IsHelmChart(path) {
		renderedManifests, err = helm.RenderChart(path, "release", values)
		if err != nil {
			log.Fatalf("Failed to render target Chart: '%s'", err)
		}
		return renderedManifests
	} else if kustomize.IsKustomize(path) {
		renderedManifests, err = kustomize.RenderKustomization(path)
		if err != nil {
			log.Fatalf("Failed to build target Kustomization: '%s'", err)
		}
		return renderedManifests
	}
	log.Fatalf("Target path is not a valid Helm Chart or Kustomization. Path may not exist in Target Ref.")
	return ""
}

// createDiff generates a unified diff string between two text inputs.
func createDiff(a, b string, fromName, toName string) string {
	edits := myers.ComputeEdits(span.URI(fromName), a, b)
	diff := gotextdiff.ToUnified(fromName, toName, a, edits)

	return fmt.Sprint(diff)
}

// colorizeDiff adds simple ANSI colors to a diff string.
// We want to see this output in a terminal or as a comment on a PR
// Fast readability is important
func colorizeDiff(diff string) string {
	var coloredDiff strings.Builder
	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		switch {
		// Standard unified diff lines
		case strings.HasPrefix(line, "+"):
			coloredDiff.WriteString(colorGreen + line + colorReset + "\n")
		case strings.HasPrefix(line, "-"):
			coloredDiff.WriteString(colorRed + line + colorReset + "\n")
		case strings.HasPrefix(line, "@@"):
			coloredDiff.WriteString(colorCyan + line + colorReset + "\n")
		// --- and +++ are headers, no special color
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			coloredDiff.WriteString(line + "\n")
		// Default (context lines, start with a space)
		default:
			coloredDiff.WriteString(line + "\n")
		}
	}

	return coloredDiff.String()
}
