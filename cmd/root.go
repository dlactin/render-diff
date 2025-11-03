package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

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

// Flag vars
var (
	valuesFlag     []string
	renderPathFlag string
	gitRefFlag     string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "render-diff",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Version: getVersion(),

	RunE: func(cmd *cobra.Command, args []string) error {
		return run("test")
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&renderPathFlag, "path", "p", "", "Relative path to the chart or kustomization directory (required)")
	rootCmd.PersistentFlags().StringVarP(&gitRefFlag, "ref", "r", "main", "Target Git ref to compare against")
	rootCmd.PersistentFlags().StringSliceVarP(&valuesFlag, "values", "v", []string{}, "Path to an additional values file (can be specified multiple times)")

	// Path is the only required flag
	err := rootCmd.MarkPersistentFlagRequired("path")
	if err != nil {
		panic(err)
	}
}

// getVersion returnb the application version
func getVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok || buildInfo.Main.Version == "" {
		return "development"
	} else {
		return buildInfo.Main.Version
	}
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

func run(string) error {
	// A local git installation is required
	_, err := exec.LookPath("git")
	if err != nil {
		log.Fatal("git not found in PATH")
	}

	if out, err := exec.Command("git", "rev-parse", "--verify", "--quiet", gitRefFlag).CombinedOutput(); err != nil {
		log.Fatalf("Invalid --ref %q: %s", gitRefFlag, strings.TrimSpace(string(out)))
	}

	log.Printf("Starting diff against git ref '%s'", gitRefFlag)

	// Get Git Root and Define Paths
	repoRoot, err := getRepoRoot()
	if err != nil {
		log.Fatal(err.Error())
	}

	// Get the absolute path from the path flag
	absPath, err := filepath.Abs(renderPathFlag)
	if err != nil {
		log.Fatalf("Failed to resolve absolute path for -path %v", err)
	}

	// Get the relative path compared to the repoRoot)
	relativePath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		log.Fatalf("Failed to resolve relative path for -path %v", err)
	}

	if strings.HasPrefix(relativePath, "..") {
		log.Fatalf("Error: The provided path '%s' (resolves to '%s') is outside the git repository root '%s'.", renderPathFlag, absPath, repoRoot)
	}

	localPath := filepath.Join(repoRoot, relativePath)

	// Resolve relative values file paths to absolute paths for the local render
	// This means we only support values files located in the path provided
	localValuesPaths := make([]string, len(valuesFlag))
	for i, v := range valuesFlag {
		localValuesPaths[i] = filepath.Join(localPath, v)
	}

	// Render Local (Feature Branch) Chart or Kustomization
	localRender, err := renderManifests(localPath, localValuesPaths)
	if err != nil {
		log.Fatalf("Failed to render path in local ref: %v", err)
	}

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
	addCmd := exec.Command("git", "worktree", "add", "-d", tempDir, gitRefFlag)
	addCmd.Dir = repoRoot // Run from the repo root
	if output, err := addCmd.CombinedOutput(); err != nil {
		log.Fatalf("Failed to create worktree for '%s': %v\nOutput: %s", gitRefFlag, err, string(output))
	}

	targetPath := filepath.Join(tempDir, relativePath)

	// Resolve values file paths for the worktree
	targetValuesPaths := make([]string, len(valuesFlag))
	for i, v := range valuesFlag {
		targetValuesPaths[i] = filepath.Join(targetPath, v)
	}

	// Render target Ref Chart or Kustomization
	targetRender, err := renderManifests(targetPath, targetValuesPaths)
	if err != nil {
		// If the path does not exist in the target ref
		// We can assume it's a new addition and diff against
		// an empty string instead.
		if os.IsNotExist(err) {
			targetRender = ""
		} else {
			log.Fatalf("Failed to render target ref manifests: %v", err)
		}
	}

	// Generate and Print Diff
	diff := createDiff(targetRender, localRender, fmt.Sprintf("%s/%s", gitRefFlag, relativePath), fmt.Sprintf("local/%s", relativePath))

	if diff == "" {
		fmt.Println("\nNo differences found between rendered manifests.")
	} else {
		fmt.Printf("\n--- Manifest Differences (%s vs. Local) ---\n", gitRefFlag)
		fmt.Println(colorizeDiff(diff))
	}

	return err
}

// renderManifests will render a Helm Chart or build a Kustomization
// and return the rendered manifests as a string
func renderManifests(path string, values []string) (string, error) {
	var renderedManifests string
	var err error

	if helm.IsHelmChart(path) {
		renderedManifests, err = helm.RenderChart(path, "release", values)
		if err != nil {
			return "", fmt.Errorf("failed to render target Chart: '%s'", err)
		}
		return renderedManifests, nil
	} else if kustomize.IsKustomize(path) {
		renderedManifests, err = kustomize.RenderKustomization(path)
		if err != nil {
			return "", fmt.Errorf("failed to build target Kustomization: '%s'", err)
		}
		return renderedManifests, nil
	}

	return "", fmt.Errorf("path: %s is not a valid Helm Chart or Kustomization", path)
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
