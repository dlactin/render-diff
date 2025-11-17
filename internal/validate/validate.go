// Package validate provides functions to validate rendered manifests
// We're using the kubeconform library here for manifest validation against
// the default schemas supported by kubeconform. Will need a way to pass
// in additional schema locations.
package validate

import (
	"fmt"
	"io"
	"strings"

	"github.com/yannh/kubeconform/pkg/resource"
	"github.com/yannh/kubeconform/pkg/validator"
)

func ValidateManifests(manifest string, debug bool) error {
	// We're not passing in any schemas here, we should grab this from an envvar
	v, err := validator.New(nil, validator.Opts{
		Strict:    true,
		Debug:     debug,
		SkipKinds: map[string]struct{}{"CustomResourceDefinition": {}},
	})
	if err != nil {
		return fmt.Errorf("error validating supplied manifest: %w", err)
	}

	// The kubeconform validator expects a file stream and not a string
	reader := strings.NewReader(manifest)
	stream := io.NopCloser(reader)

	results := v.Validate("", stream)

	// We want to ensure all the errors are captured
	// So we don't return early while there are still invalid manifests
	var errs strings.Builder
	var validationFailed bool

	for i, res := range results {
		// Build a more helpful identifier for the resource
		// We want to know which resource failed validation
		resourceID := buildResourceID(i+1, &res.Resource)

		switch res.Status {
		case validator.Invalid:
			validationFailed = true
			errs.WriteString(fmt.Sprintf(
				"  - %s is invalid:\n      %s\n",
				resourceID,
				res.Err,
			))
		case validator.Error:
			validationFailed = true
			errs.WriteString(fmt.Sprintf(
				"  - Error processing %s:\n      %s\n",
				resourceID,
				res.Err,
			))
		}
	}

	if validationFailed {
		return fmt.Errorf("manifest validation failed:\n%s", errs.String())
	}

	return nil
}

// Create a useful string for the target resource
// We want to return the Document, Kind and Name if available
func buildResourceID(index int, res *resource.Resource) string {
	sig, _ := res.Signature()

	var kind, name string
	if sig != nil {
		kind = sig.Kind
		name = sig.Name
	}

	switch {
	case kind != "" && name != "":
		return fmt.Sprintf("Document %d (Kind: %s, Name: %s)", index, kind, name)
	case kind != "":
		return fmt.Sprintf("Document %d (Kind: %s)", index, kind)
	default:
		// This is the fallback for when Kind is empty,
		return fmt.Sprintf("Document %d (at %s)", index, res.Path)
	}
}
