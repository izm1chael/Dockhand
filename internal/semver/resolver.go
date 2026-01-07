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
	constraint, err := parseConstraint(policy)
	if err != nil {
		return "", err
	}

	repo, err := parseRepo(image)
	if err != nil {
		return "", err
	}

	tags, err := listTags(ctx, repo, r.keychain)
	if err != nil {
		return "", err
	}

	tag, err := selectHighestTag(tags, constraint)
	if err != nil {
		return "", fmt.Errorf("no tags found matching policy %q for %s", policy, repo.Name())
	}

	return fmt.Sprintf("%s:%s", repo.Name(), tag), nil
}

func parseConstraint(policy string) (*mvc.Constraints, error) {
	c, err := mvc.NewConstraint(policy)
	if err != nil {
		return nil, fmt.Errorf("invalid semver policy %q: %w", policy, err)
	}
	return c, nil
}

func parseRepo(image string) (name.Repository, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return name.Repository{}, fmt.Errorf("invalid image reference %q: %w", image, err)
	}
	return ref.Context(), nil
}

func listTags(ctx context.Context, repo name.Repository, keychain authn.Keychain) ([]string, error) {
	tags, err := remote.List(repo, remote.WithAuthFromKeychain(keychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %s: %w", repo.Name(), err)
	}
	return tags, nil
}

func selectHighestTag(tags []string, constraint *mvc.Constraints) (string, error) {
	var versions []*mvc.Version
	originalTags := make(map[string]string)
	for _, t := range tags {
		v, err := mvc.NewVersion(t)
		if err != nil {
			continue
		}
		if constraint.Check(v) {
			versions = append(versions, v)
			originalTags[v.Original()] = t
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no matching versions")
	}
	sort.Sort(mvc.Collection(versions))
	highest := versions[len(versions)-1]
	tag := originalTags[highest.Original()]
	if tag == "" {
		tag = highest.Original()
	}
	return tag, nil
}
