package images

import (
	"bytes"
	"os"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/image"
	"github.com/docker/docker/layer"
	"gotest.tools/assert"
)

func TestImageService_DeltaCreate(t *testing.T) {
	daemon := &ImageService{
		referenceStore: &fakeReferenceStore{},
		imageStore:     image.NewFakeStore(),
		layerStores: map[string]layer.Store{
			"fakeOS": nil, // TODO: need something more real here!
		},
	}

	addImagesToFakeStore(t, daemon.imageStore, "001-src", "001-dest")

	options := types.ImageDeltaOptions{}
	outStream := &bytes.Buffer{}

	err := daemon.DeltaCreate("001-src", "001-dest", options, outStream)
	assert.Assert(t, err == nil)
}

func addImagesToFakeStore(t *testing.T, is image.Store, ids ...string) {
	for _, id := range ids {
		jsonBytes, err := os.ReadFile("testdata/images/" + id + ".json")
		assert.Assert(t, err == nil)

		id, err := is.Create(jsonBytes)
		assert.Assert(t, err == nil)
		assert.Assert(t, id != "")
	}
}
