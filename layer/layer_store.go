package layer // import "github.com/docker/docker/layer"

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	"github.com/docker/distribution"
	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/plugingetter"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/pkg/system"
	"github.com/docker/docker/pkg/tarsplitutils"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/vbatts/tar-split/tar/asm"
	"github.com/vbatts/tar-split/tar/storage"
)

// maxLayerDepth represents the maximum number of
// layers which can be chained together. 125 was
// chosen to account for the 127 max in some
// graphdrivers plus the 2 additional layers
// used to create a rwlayer.
const maxLayerDepth = 125

type layerStore struct {
	store       *fileMetadataStore
	driver      graphdriver.Driver
	useTarSplit bool

	layerMap map[ChainID]*roLayer
	layerL   sync.Mutex

	mounts map[string]*mountedLayer
	mountL sync.Mutex
	os     string
}

// StoreOptions are the options used to create a new Store instance
type StoreOptions struct {
	Root                      string
	MetadataStorePathTemplate string
	GraphDriver               string
	GraphDriverOptions        []string
	IDMapping                 *idtools.IdentityMapping
	PluginGetter              plugingetter.PluginGetter
	ExperimentalEnabled       bool
	OS                        string
}

// NewStoreFromOptions creates a new Store instance
func NewStoreFromOptions(options StoreOptions) (Store, int, error) {
	driver, err := graphdriver.New(options.GraphDriver, options.PluginGetter, graphdriver.Options{
		Root:                options.Root,
		DriverOptions:       options.GraphDriverOptions,
		UIDMaps:             options.IDMapping.UIDs(),
		GIDMaps:             options.IDMapping.GIDs(),
		ExperimentalEnabled: options.ExperimentalEnabled,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("error initializing graphdriver: %v", err)
	}
	logrus.Debugf("Initialized graph driver %s", driver)

	root := fmt.Sprintf(options.MetadataStorePathTemplate, driver)

	return newStoreFromGraphDriver(root, driver, options.OS)
}

// newStoreFromGraphDriver creates a new Store instance using the provided
// root directory and graph driver. The data in the root directory will be used to restore the Store.
func newStoreFromGraphDriver(root string, driver graphdriver.Driver, os string) (Store, int, error) {
	if !system.IsOSSupported(os) {
		return nil, 0, fmt.Errorf("failed to initialize layer store as operating system '%s' is not supported", os)
	}
	caps := graphdriver.Capabilities{}
	if capDriver, ok := driver.(graphdriver.CapabilityDriver); ok {
		caps = capDriver.Capabilities()
	}

	ms, err := newFSMetadataStore(root)
	if err != nil {
		return nil, 0, err
	}

	ls := &layerStore{
		store:       ms,
		driver:      driver,
		layerMap:    map[ChainID]*roLayer{},
		mounts:      map[string]*mountedLayer{},
		useTarSplit: !caps.ReproducesExactDiffs,
		os:          os,
	}

	ids, mounts, err := ms.List()
	if err != nil {
		return nil, 0, err
	}

	for _, id := range ids {
		l, err := ls.loadLayer(id)
		if err != nil {
			logrus.Debugf("Failed to load layer %s: %s", id, err)
			continue
		}
		if l.parent != nil {
			l.parent.referenceCount++
		}
	}

	for _, mount := range mounts {
		if err := ls.loadMount(mount); err != nil {
			logrus.Debugf("Failed to load mount %s: %s", mount, err)
		}
	}

	// We just created the layer store, no new transactions could have been started.
	// It's a good moment to run the clean up procedure.
	txData, err := ls.store.ListExistingTransactions()
	if err != nil {
		return nil, 0, err
	}

	// To heal already affected devices, we also try to get the unused FS layers using
	// graphdriver since previous versions of the engine were not properly persisting cacheID.
	leakedDriverLayers, err := ls.findUnreferencedDriverLayers()
	if err != nil {
		logrus.Errorf("Failed to detect leaked driver layers: %s", err)
		leakedDriverLayers = nil
	}

	// Data deletion can take time. So once we identify what needs to be deleted,
	// we start the operation in background.
	go func() {
		deletedCacheIDs := ls.prune(txData)
		gdDeletedCount := ls.deleteUnreferencedDriverLayers(leakedDriverLayers)

		if len(deletedCacheIDs) > 0 || gdDeletedCount > 0 {
			logrus.Infof("Pruned unused layers data: tx count = %d, layers referred from tx = %d, unreferenced gd layers = %d",
				len(txData), deletedCacheIDs, gdDeletedCount)
		}
	}()

	cleanupCount := len(txData)
	if cleanupCount == 0 {
		cleanupCount = len(leakedDriverLayers)
	}

	return ls, cleanupCount, nil
}

func (ls *layerStore) Driver() graphdriver.Driver {
	return ls.driver
}

func (ls *layerStore) loadLayer(layer ChainID) (*roLayer, error) {
	cl, ok := ls.layerMap[layer]
	if ok {
		return cl, nil
	}

	diff, err := ls.store.GetDiffID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff id for %s: %s", layer, err)
	}

	size, err := ls.store.GetSize(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get size for %s: %s", layer, err)
	}

	cacheID, err := ls.store.GetCacheID(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache id for %s: %s", layer, err)
	}

	parent, err := ls.store.GetParent(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent for %s: %s", layer, err)
	}

	descriptor, err := ls.store.GetDescriptor(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get descriptor for %s: %s", layer, err)
	}

	os, err := ls.store.getOS(layer)
	if err != nil {
		return nil, fmt.Errorf("failed to get operating system for %s: %s", layer, err)
	}

	if os != ls.os {
		return nil, fmt.Errorf("failed to load layer with os %s into layerstore for %s", os, ls.os)
	}

	cl = &roLayer{
		chainID:    layer,
		diffID:     diff,
		size:       size,
		cacheID:    cacheID,
		layerStore: ls,
		references: map[Layer]struct{}{},
		descriptor: descriptor,
	}

	if parent != "" {
		p, err := ls.loadLayer(parent)
		if err != nil {
			return nil, err
		}
		cl.parent = p
	}

	ls.layerMap[cl.chainID] = cl

	return cl, nil
}

func (ls *layerStore) loadMount(mount string) error {
	if _, ok := ls.mounts[mount]; ok {
		return nil
	}

	mountID, err := ls.store.GetMountID(mount)
	if err != nil {
		return err
	}

	initID, err := ls.store.GetInitID(mount)
	if err != nil {
		return err
	}

	parent, err := ls.store.GetMountParent(mount)
	if err != nil {
		return err
	}

	ml := &mountedLayer{
		name:       mount,
		mountID:    mountID,
		initID:     initID,
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if parent != "" {
		p, err := ls.loadLayer(parent)
		if err != nil {
			return err
		}
		ml.parent = p

		p.referenceCount++
	}

	ls.mounts[ml.name] = ml

	return nil
}

func (ls *layerStore) applyTar(tx *fileMetadataTransaction, ts io.Reader, parent string, layer *roLayer) error {
	digester := digest.Canonical.Digester()
	tr := io.TeeReader(ts, digester.Hash())

	rdr := tr
	if ls.useTarSplit {
		tsw, err := tx.TarSplitWriter(true)
		if err != nil {
			return err
		}
		metaPacker := storage.NewJSONPacker(tsw)
		defer tsw.Close()

		// we're passing nil here for the file putter, because the ApplyDiff will
		// handle the extraction of the archive
		rdr, err = asm.NewInputTarStream(tr, metaPacker, nil)
		if err != nil {
			return err
		}
	}

	applySize, err := ls.driver.ApplyDiff(layer.cacheID, parent, rdr)
	if err != nil {
		return err
	}

	// Discard trailing data but ensure metadata is picked up to reconstruct stream
	io.Copy(ioutil.Discard, rdr) // ignore error as reader may be closed

	layer.size = applySize
	layer.diffID = DiffID(digester.Digest())

	logrus.Debugf("Applied tar %s to %s, size: %d", layer.diffID, layer.cacheID, applySize)

	return nil
}

func (ls *layerStore) Register(ts io.Reader, parent ChainID) (Layer, error) {
	return ls.registerWithDescriptor(ts, parent, distribution.Descriptor{})
}

func (ls *layerStore) registerWithDescriptor(ts io.Reader, parent ChainID, descriptor distribution.Descriptor) (Layer, error) {
	// err is used to hold the error which will always trigger
	// cleanup of creates sources but may not be an error returned
	// to the caller (already exists).
	var err error
	var pid string
	var p *roLayer

	if string(parent) != "" {
		p = ls.get(parent)
		if p == nil {
			return nil, ErrLayerDoesNotExist
		}
		pid = p.cacheID
		// Release parent chain if error
		defer func() {
			if err != nil {
				ls.layerL.Lock()
				ls.releaseLayer(p)
				ls.layerL.Unlock()
			}
		}()
		if p.depth() >= maxLayerDepth {
			err = ErrMaxDepthExceeded
			return nil, err
		}
	}

	// Create new roLayer
	layer := &roLayer{
		parent:         p,
		cacheID:        stringid.GenerateRandomID(),
		referenceCount: 1,
		layerStore:     ls,
		references:     map[Layer]struct{}{},
		descriptor:     descriptor,
	}

	// New transaction should be persisted before we do any operations with the graph driver
	// to avoid a possibility of having an FS layer not referenced from the layer store.
	tx, err := ls.store.StartTransaction(layer.cacheID)
	if err != nil {
		return nil, err
	}

	if err = ls.driver.Create(layer.cacheID, pid, nil); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			logrus.Debugf("Cleaning up layer %s: %v", layer.cacheID, err)
			if err := ls.driver.Remove(layer.cacheID); err != nil {
				logrus.Errorf("Error cleaning up cache layer %s: %v", layer.cacheID, err)
			}
			if err := tx.Cancel(); err != nil {
				logrus.Errorf("Error canceling metadata transaction %q: %s", tx.String(), err)
			}
		}
	}()

	if err = ls.applyTar(tx, ts, pid, layer); err != nil {
		return nil, err
	}

	if layer.parent == nil {
		layer.chainID = ChainID(layer.diffID)
	} else {
		layer.chainID = createChainIDFromParent(layer.parent.chainID, layer.diffID)
	}

	if err = storeLayer(tx, layer); err != nil {
		return nil, err
	}

	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	if existingLayer := ls.getWithoutLock(layer.chainID); existingLayer != nil {
		// Set error for cleanup, but do not return the error
		err = errors.New("layer already exists")
		return existingLayer.getReference(), nil
	}

	if err = tx.Commit(layer.chainID); err != nil {
		return nil, err
	}

	ls.layerMap[layer.chainID] = layer

	return layer.getReference(), nil
}

func (ls *layerStore) getWithoutLock(layer ChainID) *roLayer {
	l, ok := ls.layerMap[layer]
	if !ok {
		return nil
	}

	l.referenceCount++

	return l
}

func (ls *layerStore) get(l ChainID) *roLayer {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	return ls.getWithoutLock(l)
}

func (ls *layerStore) Get(l ChainID) (Layer, error) {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	layer := ls.getWithoutLock(l)
	if layer == nil {
		return nil, ErrLayerDoesNotExist
	}

	return layer.getReference(), nil
}

func (ls *layerStore) Map() map[ChainID]Layer {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()

	layers := map[ChainID]Layer{}

	for k, v := range ls.layerMap {
		layers[k] = v
	}

	return layers
}

func (ls *layerStore) deleteLayer(layer *roLayer, metadata *Metadata) error {
	err := ls.driver.Remove(layer.cacheID)
	if err != nil {
		return err
	}
	err = ls.store.Remove(layer.chainID)
	if err != nil {
		return err
	}
	metadata.DiffID = layer.diffID
	metadata.ChainID = layer.chainID
	metadata.Size, err = layer.Size()
	if err != nil {
		return err
	}
	metadata.DiffSize = layer.size

	return nil
}

func (ls *layerStore) releaseLayer(l *roLayer) ([]Metadata, error) {
	depth := 0
	removed := []Metadata{}
	for {
		if l.referenceCount == 0 {
			panic("layer not retained")
		}
		l.referenceCount--
		if l.referenceCount != 0 {
			return removed, nil
		}

		if len(removed) == 0 && depth > 0 {
			panic("cannot remove layer with child")
		}
		if l.hasReferences() {
			panic("cannot delete referenced layer")
		}
		var metadata Metadata
		if err := ls.deleteLayer(l, &metadata); err != nil {
			return nil, err
		}

		delete(ls.layerMap, l.chainID)
		removed = append(removed, metadata)

		if l.parent == nil {
			return removed, nil
		}

		depth++
		l = l.parent
	}
}

func (ls *layerStore) Release(l Layer) ([]Metadata, error) {
	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	layer, ok := ls.layerMap[l.ChainID()]
	if !ok {
		return []Metadata{}, nil
	}
	if !layer.hasReference(l) {
		return nil, ErrLayerNotRetained
	}

	layer.deleteReference(l)

	return ls.releaseLayer(layer)
}

func (ls *layerStore) CreateRWLayer(name string, parent ChainID, opts *CreateRWLayerOpts) (RWLayer, error) {
	var (
		storageOpt map[string]string
		initFunc   MountInit
		mountLabel string
	)

	if opts != nil {
		mountLabel = opts.MountLabel
		storageOpt = opts.StorageOpt
		initFunc = opts.InitFunc
	}

	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	m, ok := ls.mounts[name]
	if ok {
		return nil, ErrMountNameConflict
	}

	var err error
	var pid string
	var p *roLayer
	if string(parent) != "" {
		p = ls.get(parent)
		if p == nil {
			return nil, ErrLayerDoesNotExist
		}
		pid = p.cacheID

		// Release parent chain if error
		defer func() {
			if err != nil {
				ls.layerL.Lock()
				ls.releaseLayer(p)
				ls.layerL.Unlock()
			}
		}()
	}

	m = &mountedLayer{
		name:       name,
		parent:     p,
		mountID:    ls.mountID(name),
		layerStore: ls,
		references: map[RWLayer]*referencedRWLayer{},
	}

	if initFunc != nil {
		pid, err = ls.initMount(m.mountID, pid, mountLabel, initFunc, storageOpt)
		if err != nil {
			return nil, err
		}
		m.initID = pid
	}

	createOpts := &graphdriver.CreateOpts{
		StorageOpt: storageOpt,
	}

	if err = ls.driver.CreateReadWrite(m.mountID, pid, createOpts); err != nil {
		return nil, err
	}
	if err = ls.saveMount(m); err != nil {
		return nil, err
	}

	return m.getReference(), nil
}

func (ls *layerStore) GetRWLayer(id string) (RWLayer, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	mount, ok := ls.mounts[id]
	if !ok {
		return nil, ErrMountDoesNotExist
	}

	return mount.getReference(), nil
}

func (ls *layerStore) GetMountID(id string) (string, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	mount, ok := ls.mounts[id]
	if !ok {
		return "", ErrMountDoesNotExist
	}
	logrus.Debugf("GetMountID id: %s -> mountID: %s", id, mount.mountID)

	return mount.mountID, nil
}

func (ls *layerStore) ReleaseRWLayer(l RWLayer) ([]Metadata, error) {
	ls.mountL.Lock()
	defer ls.mountL.Unlock()
	m, ok := ls.mounts[l.Name()]
	if !ok {
		return []Metadata{}, nil
	}

	if err := m.deleteReference(l); err != nil {
		return nil, err
	}

	if m.hasReferences() {
		return []Metadata{}, nil
	}

	if err := ls.driver.Remove(m.mountID); err != nil {
		logrus.Errorf("Error removing mounted layer %s: %s", m.name, err)
		m.retakeReference(l)
		return nil, err
	}

	if m.initID != "" {
		if err := ls.driver.Remove(m.initID); err != nil {
			logrus.Errorf("Error removing init layer %s: %s", m.name, err)
			m.retakeReference(l)
			return nil, err
		}
	}

	if err := ls.store.RemoveMount(m.name); err != nil {
		logrus.Errorf("Error removing mount metadata: %s: %s", m.name, err)
		m.retakeReference(l)
		return nil, err
	}

	delete(ls.mounts, m.Name())

	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	if m.parent != nil {
		return ls.releaseLayer(m.parent)
	}

	return []Metadata{}, nil
}

func (ls *layerStore) saveMount(mount *mountedLayer) error {
	if err := ls.store.SetMountID(mount.name, mount.mountID); err != nil {
		return err
	}

	if mount.initID != "" {
		if err := ls.store.SetInitID(mount.name, mount.initID); err != nil {
			return err
		}
	}

	if mount.parent != nil {
		if err := ls.store.SetMountParent(mount.name, mount.parent.chainID); err != nil {
			return err
		}
	}

	ls.mounts[mount.name] = mount

	return nil
}

func (ls *layerStore) initMount(graphID, parent, mountLabel string, initFunc MountInit, storageOpt map[string]string) (string, error) {
	// Use "<graph-id>-init" to maintain compatibility with graph drivers
	// which are expecting this layer with this special name. If all
	// graph drivers can be updated to not rely on knowing about this layer
	// then the initID should be randomly generated.
	initID := fmt.Sprintf("%s-init", graphID)

	createOpts := &graphdriver.CreateOpts{
		MountLabel: mountLabel,
		StorageOpt: storageOpt,
	}

	if err := ls.driver.CreateReadWrite(initID, parent, createOpts); err != nil {
		return "", err
	}
	p, err := ls.driver.Get(initID, "")
	if err != nil {
		return "", err
	}

	if err := initFunc(p); err != nil {
		ls.driver.Put(initID)
		return "", err
	}

	if err := ls.driver.Put(initID); err != nil {
		return "", err
	}

	return initID, nil
}

func (ls *layerStore) getTarStream(rl *roLayer) (io.ReadCloser, error) {
	if !ls.useTarSplit {
		var parentCacheID string
		if rl.parent != nil {
			parentCacheID = rl.parent.cacheID
		}

		return ls.driver.Diff(rl.cacheID, parentCacheID)
	}

	r, err := ls.store.TarSplitReader(rl.chainID)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		err := ls.assembleTarTo(rl.cacheID, r, nil, pw)
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()

	return pr, nil
}

func (ls *layerStore) getTarSeekStream(rl *roLayer) (ioutils.ReadSeekCloser, error) {
	if !ls.useTarSplit {
		return nil, fmt.Errorf("unsupported backend driver for seek streams")
	}

	diffDriver, ok := ls.driver.(graphdriver.DiffGetterDriver)
	if !ok {
		diffDriver = &naiveDiffPathDriver{ls.driver}
	}

	metadata, err := ls.store.TarSplitReader(rl.chainID)
	if err != nil {
		return nil, err
	}
	defer metadata.Close()

	// get our relative path to the container
	fileGetCloser, err := diffDriver.DiffGetter(rl.cacheID)
	if err != nil {
		return nil, err
	}

	metaUnpacker := storage.NewJSONUnpacker(metadata)

	stream, err := tarsplitutils.NewRandomAccessTarStream(fileGetCloser, metaUnpacker)
	if err != nil {
		return nil, err
	}

	return ioutils.NewReadSeekCloserWrapper(stream, func() error {
		return fileGetCloser.Close()
	}), nil
}

func (ls *layerStore) assembleTarTo(graphID string, metadata io.ReadCloser, size *int64, w io.Writer) error {
	diffDriver, ok := ls.driver.(graphdriver.DiffGetterDriver)
	if !ok {
		diffDriver = &naiveDiffPathDriver{ls.driver}
	}

	defer metadata.Close()

	// get our relative path to the container
	fileGetCloser, err := diffDriver.DiffGetter(graphID)
	if err != nil {
		return err
	}
	defer fileGetCloser.Close()

	metaUnpacker := storage.NewJSONUnpacker(metadata)
	upackerCounter := &unpackSizeCounter{metaUnpacker, size}
	logrus.Debugf("Assembling tar data for %s", graphID)
	return asm.WriteOutputTarStream(fileGetCloser, upackerCounter, w)
}

func (ls *layerStore) Cleanup() error {
	return ls.driver.Cleanup()
}

func (ls *layerStore) DriverStatus() [][2]string {
	return ls.driver.Status()
}

func (ls *layerStore) DriverName() string {
	return ls.driver.String()
}

func (ls *layerStore) prune(txData []fileMetadataTxData) []string {
	treatedCacheIDs := make([]string, 0, len(txData))

	for _, tx := range txData {
		if cacheID, err := tx.GetCacheID(); err == nil {
			if err := ls.driver.Remove(cacheID); err == nil {
				logrus.Debugf("Deleted layer %s", cacheID)
				treatedCacheIDs = append(treatedCacheIDs, cacheID)
			} else {
				logrus.Debugf("Failed to delete layer %s: %s", cacheID, err)
			}
		} else {
			logrus.Errorf("Failed to read cacheID from tx [%s] data: %s", tx, err)
		}
		if err := tx.Delete(); err != nil {
			logrus.Errorf("Failed to delete tx [%s] data that should be pruned: %s", tx, err)
		}
	}

	return treatedCacheIDs
}

func (ls *layerStore) findUnreferencedDriverLayers() ([]string, error) {
	d, supported := ls.driver.(graphdriver.InspectableDriver)
	if !supported {
		return nil, nil
	}
	cacheIDs, err := d.List()
	if err != nil {
		return nil, err
	}

	ls.layerL.Lock()
	defer ls.layerL.Unlock()
	ls.mountL.Lock()
	defer ls.mountL.Unlock()

	diff := len(cacheIDs) - len(ls.layerMap) - len(ls.mounts)
	if diff == 0 {
		return nil, nil
	}
	if diff < 0 {
		return nil, fmt.Errorf("driver [%s] layers count (%d) is smaller than number of engine layers (%d + %d)",
			ls.driver, len(cacheIDs), len(ls.layerMap), len(ls.mounts))
	}
	unused := make([]string, 0, diff)

	usedLayers := make(map[string]struct{}, len(ls.layerMap)+len(ls.mounts)*2)
	used := struct{}{}
	for _, v := range ls.layerMap {
		usedLayers[v.cacheID] = used
	}
	for _, v := range ls.mounts {
		usedLayers[v.mountID] = used
		if len(v.initID) > 0 {
			usedLayers[v.initID] = used
		}
	}

	for _, cacheID := range cacheIDs {
		if _, used := usedLayers[cacheID]; !used {
			unused = append(unused, cacheID)
		}
	}

	return unused, nil
}

func (ls *layerStore) deleteUnreferencedDriverLayers(ids []string) int {
	total := 0
	for _, leakedCachedID := range ids {
		if err := ls.driver.Remove(leakedCachedID); err == nil {
			logrus.Debugf("Deleted leaked driver layer %s", leakedCachedID)
			total++
		}
	}
	return total
}

type naiveDiffPathDriver struct {
	graphdriver.Driver
}

type fileGetPutter struct {
	storage.FileGetter
	driver graphdriver.Driver
	id     string
}

func (w *fileGetPutter) Close() error {
	return w.driver.Put(w.id)
}

func (n *naiveDiffPathDriver) DiffGetter(id string) (graphdriver.FileGetCloser, error) {
	p, err := n.Driver.Get(id, "")
	if err != nil {
		return nil, err
	}
	return &fileGetPutter{storage.NewPathFileGetter(p.Path()), n.Driver, id}, nil
}
