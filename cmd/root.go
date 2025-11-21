// Package cmd implements the command-line interface for rdv
// using the Cobra library.
package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dlactin/rdv/internal/diff"
	"github.com/dlactin/rdv/internal/git"
	"github.com/dlactin/rdv/internal/validate"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"
)

// Package vars
// Includes flag vars and some set during PreRun
var (
	valuesFlag       []string
	renderPathFlag   string
	gitRefFlag       string
	updateFlag       bool
	debugFlag        bool
	validateFlag     bool
	semanticDiffFlag bool
	plainFlag        bool

	repoRoot string
	fullRef  string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "rdv",
	Short: "A CLI tool to render Helm/Kustomize, validate and print the diff of manifests between a local revision and target ref.",
	Long: `rdv provides a fast and local preview of your Kubernetes manifest changes. With basic Helm linting and Manifest validation.

It renders your local Helm charts or Kustomize overlays, validates the output against Kubernetes schemas (via kubeconform),
and generates a colored diff comparing your local changes against a target Git reference (e.g., 'main').`,
	Version: getVersion(),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		log.SetFlags(0) // Disabling timestamps for log output

		// A local git installation is required
		_, err := exec.LookPath("git")
		if err != nil {
			return fmt.Errorf("git not found in PATH: %w", err)
		}

		// Get Git repository root
		repoRoot, err = git.GetRepoRoot()
		if err != nil {
			return err
		}

		// Try to find the upstream for our target ref
		upstreamRef := exec.Command("git", "rev-parse", "--abbrev-ref", gitRefFlag+"@{u}")
		upstreamRef.Dir = repoRoot

		output, err := upstreamRef.CombinedOutput()
		if err == nil {
			fullRef = strings.TrimSpace(string(output))
			if debugFlag {
				log.Printf("Found upstream for '%s', using '%s'", gitRefFlag, fullRef)
			}
		} else {
			fullRef = gitRefFlag
			if debugFlag {
				log.Printf("No upstream found for '%s', using local ref", fullRef)
			}
		}

		// Validate our git ref exists
		validateRef := exec.Command("git", "rev-parse", "--verify", "--quiet", fullRef)
		validateRef.Dir = repoRoot

		if out, err := validateRef.CombinedOutput(); err != nil {
			return fmt.Errorf("invalid or non-existent ref %q: %s", fullRef, strings.TrimSpace(string(out)))
		}

		return nil
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		log.Printf("Starting diff against git ref '%s':", fullRef)

		// Get the absolute path from the path flag
		absPath, err := filepath.Abs(renderPathFlag)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for -path %w", err)
		}

		// Get the relative path compared to the repoRoot)
		relativePath, err := filepath.Rel(repoRoot, absPath)
		if err != nil {
			return fmt.Errorf("failed to resolve relative path for -path %w", err)
		}

		if strings.HasPrefix(relativePath, "..") {
			return fmt.Errorf("the provided path '%s' (resolves to '%s') is outside the git repository root '%s'", renderPathFlag, absPath, repoRoot)
		}

		localPath := filepath.Join(repoRoot, relativePath)

		// Resolve relative values file paths to absolute paths for the local render
		// This means we only support values files located in the path provided
		localValuesPaths := make([]string, len(valuesFlag))
		for i, v := range valuesFlag {
			localValuesPaths[i] = filepath.Join(localPath, v)
		}

		// Setup temporary work tree for diffs
		tempDir, cleanup, err := git.SetupWorkTree(repoRoot, fullRef)
		if err != nil {
			return err
		}
		// We want this to run after we have generated our diffs
		defer cleanup()

		targetPath := filepath.Join(tempDir, relativePath)

		// Resolve values file paths for the worktree
		targetValuesPaths := make([]string, len(valuesFlag))
		for i, v := range valuesFlag {
			targetValuesPaths[i] = filepath.Join(targetPath, v)
		}

		// Create localRender and targetRender outside of goroutines
		// Create errgroup for chart/kustomization rendering
		var localRender, targetRender string
		g := new(errgroup.Group)

		// We only lint our local version
		// Render local Chart or Kustomization
		g.Go(func() error {
			localRender, err = diff.RenderManifests(localPath, localValuesPaths, debugFlag, updateFlag, true)
			if err != nil {
				return fmt.Errorf("failed to render path in local ref: %w", err)
			}

			// Run local rendered manifests through kubeconform if --validate flag is passed
			if validateFlag {
				err = validate.ValidateManifests(localRender, debugFlag)
				if err != nil {
					return err
				}
			}
			return nil
		})

		// Render target Ref Chart or Kustomization
		g.Go(func() error {
			targetRender, err = diff.RenderManifests(targetPath, targetValuesPaths, debugFlag, updateFlag, false)
			if err != nil {
				// If the path does not exist in the target ref
				// We can assume it's a new addition and diff against
				// an empty string instead.
				if os.IsNotExist(err) {
					targetRender = ""
				} else {
					return fmt.Errorf("failed to render target ref manifests: %w", err)
				}
			}
			return nil
		})

		// Ensure both rendering goroutines have finished before creating our diff
		err = g.Wait()
		if err != nil {
			return err
		}

		if semanticDiffFlag {
			// We are using a more complex diff engine (dyff) which is better suited for k8s manifest comparison
			renderedDiff, err := diff.CreateSemanticDiff(targetRender, localRender, fmt.Sprintf("%s/%s", fullRef, relativePath), fmt.Sprintf("local/%s", relativePath), plainFlag)
			if err != nil {
				return fmt.Errorf("error creating dyff: %w", err)
			}

			if len(renderedDiff.Diffs) == 0 {
				fmt.Println("\nNo differences found between rendered manifests.")
				return nil
			} else {
				fmt.Printf("\n--- Diff (%s vs. local) ---", fullRef)
				err := renderedDiff.WriteReport(os.Stdout)
				if err != nil {
					return err
				}
			}
		} else {
			// Generate and Print our simple diff
			// This is better suited for github comments, or small changes
			renderedDiff := diff.CreateDiff(targetRender, localRender, fmt.Sprintf("%s/%s", fullRef, relativePath), fmt.Sprintf("local/%s", relativePath))

			if renderedDiff == "" {
				fmt.Println("\nNo differences found between rendered manifests.")
			} else {
				fmt.Printf("\n--- Diff (%s vs. local) ---\n", fullRef)
				fmt.Println(diff.ColorizeDiff(renderedDiff, plainFlag))

			}
		}
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// Create a context that is cancelled on an interrupt signal
	// We want to ensure the work tree cleanup is run even if interrupted.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

// Initializes our RootCmd with the flags below.
func init() {
	// Core flags
	coreFlags := pflag.NewFlagSet("generic", pflag.ContinueOnError)
	coreFlags.SortFlags = false

	coreFlags.StringVarP(&renderPathFlag, "path", "p", ".", "Relative path to the chart or kustomization directory")
	coreFlags.StringVarP(&gitRefFlag, "ref", "r", "main", "Target Git ref to compare against. Will try to find its remote-tracking branch (e.g., origin/main)")
	coreFlags.BoolVarP(&validateFlag, "validate", "v", false, "Validate rendered manifests with kubeconform")

	// Helm flags
	helmFlags := pflag.NewFlagSet("helm", pflag.ContinueOnError)
	helmFlags.SortFlags = false

	helmFlags.StringSliceVarP(&valuesFlag, "values", "f", []string{}, "Path to an additional values file (can be specified multiple times)")
	helmFlags.BoolVarP(&updateFlag, "update", "u", false, "Update Helm chart dependencies. Required if lockfile does not match dependencies")

	// Output flags
	outputFlags := pflag.NewFlagSet("output", pflag.ContinueOnError)
	outputFlags.SortFlags = false

	outputFlags.BoolVarP(&semanticDiffFlag, "semantic", "s", false, "Enable semantic diffing of k8s manifests (using dyff)")
	outputFlags.BoolVarP(&plainFlag, "plain", "", false, "Output in plain style without any highlighting")
	outputFlags.BoolVarP(&debugFlag, "debug", "", false, "Enable verbose logging for debugging")

	// Add our custom flagsets to our rootCMD
	rootCmd.Flags().AddFlagSet(coreFlags)
	rootCmd.Flags().AddFlagSet(helmFlags)
	rootCmd.Flags().AddFlagSet(outputFlags)

	// Clean up the help message to print our flag sets
	rootCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		out := cmd.OutOrStdout()

		// Check for the auto-generated version flag
		if vFlag := cmd.Flags().Lookup("version"); vFlag != nil {
			if outputFlags.Lookup("version") == nil {
				outputFlags.AddFlag(vFlag)
			}
		}
		// Check for the auto-generated help flag
		if hFlag := cmd.Flags().Lookup("help"); hFlag != nil {
			if outputFlags.Lookup("help") == nil {
				outputFlags.AddFlag(hFlag)
			}
		}

		// Print the standard Usage header
		_, err := fmt.Fprintf(out, "Usage:\n  %s [flags]\n", cmd.Use)
		if err != nil {
			return err
		}

		// Print global flags
		_, _ = fmt.Fprintf(out, "\nCore Flags:\n")
		_, err = fmt.Fprint(out, coreFlags.FlagUsages())
		if err != nil {
			return err
		}

		// Print Helm flags
		_, _ = fmt.Fprintf(out, "\nHelm Flags:\n")
		_, err = fmt.Fprint(out, helmFlags.FlagUsages())
		if err != nil {
			return err
		}

		// Print output flags
		_, _ = fmt.Fprintf(out, "\nOutput Flags:\n")
		_, err = fmt.Fprint(out, outputFlags.FlagUsages())
		if err != nil {
			return err
		}

		return nil
	})

}
