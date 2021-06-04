package image

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/layer"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/pkg/errors"
)

// fakeStore is a fake implementation of an image.Store meant to be used for
// testing.
//
// The fakeStore expects to find certain things on specific places:
//
// - testdata/images/abcd.json: Description of the image with ID abcd.
//
// - testdata/layers/1234.tar: Contents of the layer with diffID 1234.
//
// With regards to the IDs: fakeStore does not use a real hash, so IDs can be
// more descriptive than random hex string. Any string composed of lowercase
// alphanumeric characters and dashes (-) can be used as an ID. When a hash
// algorithm name must be specified, we use "faked" (meaning "fake digest"). So
// "faked:my-fake-id-5" is an example ID including the hash.
//
// Though fakeStore itself is a private type, we sadly need to implement it in
// the image package and export its constructor NewFakeStore(), because
// otherwise we cannot set the private Image.computedID field on the Get method.

type fakeStore struct {
	// images maps the IDs known by this store to the image themselves.
	images map[ID]*Image
}

// NewFakeStore creates a new fake image.Store.
func NewFakeStore() Store {
	return &fakeStore{
		images: map[ID]*Image{},
	}
}

// Create adds a new image to the fake store.
func (is *fakeStore) Create(config []byte) (ID, error) {
	img, err := fakeImageFromJSON(config)
	if err != nil {
		return "", err
	}
	id := img.ID()
	is.images[id] = img
	return id, nil
}

// Get returns an image given its ID.
func (is *fakeStore) Get(id ID) (*Image, error) {
	img, ok := is.images[id]
	if !ok {
		return nil, fmt.Errorf("unknown fake image ID %q", id)
	}
	return img, nil
}

// TODO: Not sure what exactly we should have on a real TarSeekStream. Here, I
// am simply returning a ReadSeekCloser that goes over a concatenation of all
// tar layers. (And looking at them just as the sequence of bytes in the tar
// file, without looking inside of it in any way).
func (is *fakeStore) GetTarSeekStream(id ID) (ioutils.ReadSeekCloser, error) {
	img, err := is.Get(id)
	if err != nil {
		return nil, err
	}

	var result ioutils.ReadSeekCloser
	for _, diffID := range img.RootFS.DiffIDs {
		segs := strings.Split(diffID.String(), ":")
		if len(segs) != 2 {
			return nil, fmt.Errorf("bad fake layer DiffID %q", id)
		}
		if segs[0] != "faked" {
			return nil, fmt.Errorf("can only use %q hash, got %q", "faked", id)
		}

		f, err := os.Open("testdata/layers/" + segs[1] + ".tar")
		if err != nil {
			return nil, err
		}
		if result == nil {
			result = f
		} else {
			result, err = ioutils.ConcatReadSeekClosers(result, f)
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (is *fakeStore) Delete(id ID) ([]layer.Metadata, error) {
	return nil, errors.New("called mock method")
}

func (is *fakeStore) Search(partialID string) (ID, error) {
	return "", errors.New("called mock method")
}

func (is *fakeStore) SetParent(id ID, parent ID) error {
	return errors.New("called mock method")
}

func (is *fakeStore) GetParent(id ID) (ID, error) {
	return "", errors.New("called mock method")
}

func (is *fakeStore) SetLastUpdated(id ID) error {
	return errors.New("called mock method")
}

func (is *fakeStore) GetLastUpdated(id ID) (time.Time, error) {
	return time.Time{}, errors.New("called mock method")
}

func (is *fakeStore) Children(id ID) []ID {
	return []ID{}
}

func (is *fakeStore) Map() map[ID]*Image {
	return map[ID]*Image{}
}

func (is *fakeStore) Heads() map[ID]*Image {
	return map[ID]*Image{}
}

func (is *fakeStore) Len() int {
	return len(is.images)
}

func fakeImageFromJSON(rawJSON []byte) (*Image, error) {
	var img Image
	err := json.Unmarshal(rawJSON, &img)
	if err != nil {
		return nil, err
	}
	img.computedID = ID(img.V1Image.ID)
	return &img, nil
}
