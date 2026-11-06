//go:build interop

package interop

import (
	"regexp"
	"testing"
)

var digestPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

func TestImagesPinnedByDigest(t *testing.T) {
	images := map[string]string{"helper": alpineImage}
	for _, r := range resolvers {
		images[r.name] = r.image
	}
	for name, image := range images {
		if !digestPattern.MatchString(image) {
			t.Errorf("image of %s is %q, want a reference that ends in @sha256:<64 hex digits>", name, image)
		}
	}
}
