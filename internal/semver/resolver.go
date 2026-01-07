package semver

import (
	"context"
	"fmt"
	"sort"

	mvc "github.com/Masterminds/semver/v3"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type Resolver struct {
	// auth can be enhanced to support custom static creds if needed,
	// currently relies on standard docker config (~/.docker/config.json)
	keychain authn.Keychain
}

func NewResolver() *Resolver {
	return &Resolver{
		keychain: authn.DefaultKeychain,
	}
}

// Resolve returns the best matching image tag based on the provided policy.
// image: "postgres:14.1"
// policy: "14.x" or "^14.0"
// Returns: "postgres:14.5" (if 14.5 is latest)
func (r *Resolver) Resolve(ctx context.Context, image string, policy string) (string, error) {
	// 1. Parse the policy constraint
	constraint, err := mvc.NewConstraint(policy)
	if err != nil {
		return "", fmt.Errorf("invalid semver policy %q: %w", policy, err)
	}

	// 2. Parse the repository name
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", image, err)
	}
	repo := ref.Context()

	// 3. List all tags from the registry
	// Note: We use the context to allow timeouts/cancellations
	tags, err := remote.List(repo, remote.WithAuthFromKeychain(r.keychain), remote.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to list tags for %s: %w", repo.Name(), err)
	}

	// 4. Filter and sort tags
	var versions []*mvc.Version
	// Keep a map to look up the original tag string from the version object
	// (preserves 'v' prefix if present)
	originalTags := make(map[string]string)

	for _, t := range tags {
		v, err := mvc.NewVersion(t)
		if err != nil {
			continue // skip non-semver tags (e.g. "latest", "alpine")
		}
		if constraint.Check(v) {
			versions = append(versions, v)
			originalTags[v.Original()] = t
		}
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("no tags found matching policy %q for %s", policy, repo.Name())
	}

	// Sort to find the highest version
	sort.Sort(mvc.Collection(versions))
	highest := versions[len(versions)-1]

	// Reconstruct the full image reference
	// We use originalTags to respect the registry's exact formatting (e.g. v1.0 vs 1.0)
	tag := originalTags[highest.Original()]
	// Handle the case where semver.Original() might differ slightly if parsed/reformatted
	if tag == "" {
		tag = highest.Original()
	}

	return fmt.Sprintf("%s:%s", repo.Name(), tag), nil
}
