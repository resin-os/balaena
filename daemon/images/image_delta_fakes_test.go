package images

import (
	"fmt"
	"strings"

	"github.com/docker/distribution/reference"
	dockerref "github.com/docker/docker/reference"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

//
// Reference Store
//

type fakeReferenceStore struct{}

func (rs *fakeReferenceStore) Get(ref reference.Named) (digest.Digest, error) {
	parts := strings.Split(ref.Name(), "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected fake reference format: %q", ref)
	}
	return digest.Digest("faked:" + parts[2]), nil
}

func (rs *fakeReferenceStore) References(id digest.Digest) []reference.Named {
	return []reference.Named{}
}

func (rs *fakeReferenceStore) ReferencesByName(ref reference.Named) []dockerref.Association {
	return []dockerref.Association{}
}

func (rs *fakeReferenceStore) AddTag(ref reference.Named, id digest.Digest, force bool) error {
	return errors.New("called mock method")
}

func (rs *fakeReferenceStore) AddDigest(ref reference.Canonical, id digest.Digest, force bool) error {
	return errors.New("called mock method")
}

func (rs *fakeReferenceStore) Delete(ref reference.Named) (bool, error) {
	return false, errors.New("called mock method")

}
