package layer

import (
	"reflect"
	"sort"
	"testing"

	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/containerfs"
	"gotest.tools/skip"
)

func TestLayerStore_pruneOnCreate(t *testing.T) {
	s, _, cleanupOriginal := newTestStore(t)
	defer cleanupOriginal()

	lStore, ok := s.(*layerStore)
	if !ok {
		t.Fatalf("Unexpected store implementation %s", s)
	}

	_, err := lStore.store.StartTransaction("test-prune-on-start")
	if err != nil {
		t.Fatal(err)
	}

	_, _, cleanup := newTestStoreWithLayersCleanup(t, lStore.store.root, 1)
	defer cleanup()
}

func TestLayerStore_prune(t *testing.T) {
	s, _, cleanup := newTestStore(t)
	defer cleanup()

	lStore, ok := s.(*layerStore)
	if !ok {
		t.Fatalf("Unexpected store implementation %s", s)
	}

	// Start new transaction with cacheID that is not committed or canceled.
	_, err := lStore.store.StartTransaction("test-prune")
	if err != nil {
		t.Fatal(err)
	}

	txData, err := lStore.store.ListExistingTransactions()
	if err != nil {
		t.Fatal(err)
	}
	cacheIDs := lStore.prune(txData)
	if len(cacheIDs) != 1 {
		t.Errorf("1 directory was expected to be removed, but got %s", cacheIDs)
	}
}

func createGraphDriverLayer(t *testing.T, lStore *layerStore, name string) {
	err := lStore.Driver().Create(name, "", nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLayerStore_unreferencedDriverLayers(t *testing.T) {
	s, _, cleanup := newTestStore(t)
	defer cleanup()
	skip.If(t, s.DriverName() != "aufs" && s.DriverName() != "overlay2" && s.DriverName() != "vfs",
		"aufs, overlay2, and vfs drivers are supported only, got %s", s.DriverName())

	lStore, ok := s.(*layerStore)
	if !ok {
		t.Fatalf("Unexpected store implementation %s", s)
	}

	createGraphDriverLayer(t, lStore, "test-leaked-layer1")
	createGraphDriverLayer(t, lStore, "test-leaked-layer2")

	_, err := lStore.CreateRWLayer("test-container-1", "", &CreateRWLayerOpts{
		InitFunc: func(root containerfs.ContainerFS) error {
			return nil
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Create RW (container) layer without init func - this will not create an -init layer.
	_, err = lStore.CreateRWLayer("test-container-2", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2 leaked layers + 2 containers + init layer for one of container.
	if ids, _ := lStore.driver.(graphdriver.InspectableDriver).List(); len(ids) != 5 {
		t.Fatalf("Unexpected graphdriver storage state: %s", ids)
	}

	txData, err := lStore.store.ListExistingTransactions()
	if err != nil {
		t.Fatal(err)
	}
	if len(txData) != 0 {
		t.Errorf("No existing transactions were expected, but got %s", txData)
	}

	ids, err := lStore.findUnreferencedDriverLayers()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"test-leaked-layer1", "test-leaked-layer2"}) {
		t.Errorf("Leaked layers are incorrectly detected: %s", ids)
	}

	n := lStore.deleteUnreferencedDriverLayers(ids)
	if n != 2 {
		t.Errorf("2 layers were expected to be deleted, but got %d", n)
	}

	for _, cacheID := range ids {
		if lStore.Driver().Exists(cacheID) {
			t.Errorf("Layer %s was not deleted", cacheID)
		}
	}
}
