package daemon // import "github.com/docker/docker/daemon"

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"sync/atomic"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/sirupsen/logrus"
)

const overlay2 = "overlay2"

var pathOverlayIDRegex = regexp.MustCompile(".+/overlay2/([a-z0-9]+).*")

// EngineCheck returns information about the daemon data disk usage
func (daemon *Daemon) EngineCheck(ctx context.Context) (int, error) {

	// FIXME: check if InspectableDriver is supported (overlay2) and get driver
	driver, supported := ls.driver.(graphdriver.InspectableDriver)
	if !supported {
		return nil, nil
	}

	if !atomic.CompareAndSwapInt32(&daemon.engineCheckRunning, 0, 1) {
		return 0, fmt.Errorf("an engine check operation is already running")
	}
	defer atomic.StoreInt32(&daemon.engineCheckRunning, 0)

	usageData := make(usageInfo)

	// Retrieve container list
	containers, err := daemon.Containers(&types.ContainerListOptions{
		Size: true,
		All:  true,
	})
	if err != nil {
		return 0, fmt.Errorf("Failed to retrieve container list: %v", err)
	}

	for _, cSummary := range containers {
		if container, err := daemon.ContainerInspectCurrent(cSummary.ID, false); err == nil {
			usageData.addContainer(*container, OverlaysFromGraphDriver(container.GraphDriver))
		} else {
			log.Fatalf("Failed to inspect container %s: %s", cSummary.ID, err)
		}
	}

	// Get all top images with extra attributes
	images, err := daemon.imageService.Images(filters.NewArgs(), false, true)
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve image list: %v", err)
	}

	for _, imgSummary := range images {
		if image, err := daemon.imageService.LookupImage(imgSummary.ID); err == nil {
			usageData.addImage(*image, OverlaysFromGraphDriver(image.GraphDriver))
		} else {
			log.Fatalf("Failed to inspect image %s: %s", imgSummary.ID, err)
		}
	}

	// FIXME: call List() from overlay.go to get list of overlay2 files (layers)
	allIds, err := driver.List()
	if err != nil {
		return 0, fmt.Errorf("Failed to list all overlays: %s", err)
	}

	unused := usageData.selectUnused(allIds)

	for _, id := range unused {
		logrus.Debugf("Removing unused layer %s", id)
		// FIXME: call Remove() function from overlay2.go
		driver.Remove(id)
	}

	return len(unused), nil
}

type overlayUsageInfo struct {
	overlayID      string
	imageIds       []string
	imageNames     []string
	containerIds   []string
	containerNames []string
}

type usageInfo map[string]*overlayUsageInfo

func (ui usageInfo) lookup(overlayID string) *overlayUsageInfo {
	oui, present := ui[overlayID]
	if !present {
		oui = &overlayUsageInfo{overlayID: overlayID}
		ui[overlayID] = oui
	}
	return oui
}

func (ui usageInfo) addImage(image types.ImageInspect, overlays OverlayIDSet) {
	for overlayID := range overlays {
		oui := ui.lookup(overlayID)
		oui.imageIds = append(oui.imageIds, image.ID)
		name := ""
		if len(image.RepoTags) > 0 {
			name = image.RepoTags[0]
		}
		oui.imageNames = append(oui.imageNames, name)
	}
}

func (ui usageInfo) addContainer(container types.ContainerJSON, overlays OverlayIDSet) {
	for overlayID := range overlays {
		oui := ui.lookup(overlayID)
		oui.containerIds = append(oui.containerIds, container.ID)
		oui.containerNames = append(oui.containerNames, container.Name)
	}
}

func (ui usageInfo) selectUnused(ids OverlayIDSet) []string {
	var res []string
	for id := range ids {
		if _, present := ui[id]; !present {
			res = append(res, id)
		}
	}
	sort.Strings(res)
	return res
}

// OverlayIDSet represents a set of overlay IDs.
type OverlayIDSet map[string]struct{}

// Slice creates a string slice that contains all IDs in the set.
func (s OverlayIDSet) Slice() []string {
	res := make([]string, len(s))
	index := 0
	for id := range s {
		res[index] = id
		index++
	}
	return res
}

func (s OverlayIDSet) add(id string) {
	s[id] = struct{}{}
}

// OverlaysFromGraphDriver extracts a set of OverlayFS overlay IDs mentioned in the GraphDriver info.
func OverlaysFromGraphDriver(gd types.GraphDriverData) OverlayIDSet {
	if gd.Name != overlay2 {
		return nil
	}
	res := make(OverlayIDSet)
	for _, str := range gd.Data {
		for _, ref := range filepath.SplitList(str) {
			if match := pathOverlayIDRegex.FindStringSubmatch(ref); match != nil {
				res.add(match[1])
			}
		}
	}
	return res
}
