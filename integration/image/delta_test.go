package image

import (
	"bufio"
	"context"
	"io"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	apiclient "github.com/docker/docker/client"
	"gotest.tools/assert"
)

// TestDeltaCreate creates a delta and checks if it exists
func TestDeltaCreate(t *testing.T) {
	var (
		base   = "busybox:1.24"
		target = "busybox:1.29"
		delta  = "busybox:delta-1.24-1.29"
	)

	var (
		err    error
		rc     io.ReadCloser
		ctx    = context.Background()
		client = testEnv.APIClient()
	)

	pullBaseAndTargetImages(t, client, base, target)

	rc, err = client.ImageDelta(ctx,
		base,
		target,
		types.ImageDeltaOptions{
			Tag: delta,
		})
	assert.NilError(t, err)
	io.Copy(ioutil.Discard, rc)
	rc.Close()

	inspectDelta, _, err := client.ImageInspectWithRaw(ctx, delta)
	assert.NilError(t, err)

	inspectBase, _, err := client.ImageInspectWithRaw(ctx, base)
	assert.NilError(t, err)
	assert.Assert(t, inspectDelta.Config.Labels["io.resin.delta.base"] == inspectBase.ID)
}

// TestDeltaCreateDestinationLock triggers a delta generation job, waits for it
// to start and removes the destination image.
//
// Until 1e83de47e32ac0caf3b4e02094aeb76da28b90b3 this would remove unprocessed
// destination layers and break delta generation.
func TestDeltaCreateDestinationLock(t *testing.T) {
	var (
		base   = "debian:8"
		target = "debian:10"
		delta  = "debian:delta"
	)

	var (
		err    error
		rc     io.ReadCloser
		ctx    = context.Background()
		client = testEnv.APIClient()
	)

	pullBaseAndTargetImages(t, client, base, target)

	rc, err = client.ImageDelta(ctx,
		base,
		target,
		types.ImageDeltaOptions{
			Tag: delta,
		})
	assert.NilError(t, err)
	defer rc.Close()

	var (
		waitFingerprinting = make(chan struct{})
		waitDelta          = make(chan struct{})
	)
	go func() {
		defer close(waitFingerprinting)
		defer close(waitDelta)
		for br := bufio.NewReader(rc); ; {
			line, err := br.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				t.Fatal(err)
			}
			if strings.Contains(line, "Fingerprint complete") {
				waitFingerprinting <- struct{}{}
			}
			if strings.Contains(line, "layer does not exist") {
				t.Fail()
			}
		}
	}()

	inspectTarget, _, err := client.ImageInspectWithRaw(ctx, target)

	<-waitFingerprinting
	deleted, err := client.ImageRemove(ctx, target, types.ImageRemoveOptions{})
	assert.NilError(t, err)
	for _, item := range deleted {
		for i := 0; i < len(inspectTarget.RootFS.Layers); i++ {
			assert.Assert(t, item.Deleted != inspectTarget.RootFS.Layers[i], "deleted target image layer")
		}
	}

	<-waitDelta
	inspectDelta, _, err := client.ImageInspectWithRaw(ctx, delta)
	assert.NilError(t, err)

	inspectBase, _, err := client.ImageInspectWithRaw(ctx, base)
	assert.NilError(t, err)
	assert.Assert(t, inspectDelta.Config.Labels["io.resin.delta.base"] == inspectBase.ID)
}

// TestDeltaSizes checks if the sizes of generated deltas are within the
// expected margins. This test is designed to catch regressions in the delta
// sizes.
//
// The "expected margins" (wantRatio) were defined empirically so that they are
// close to the values we were getting by the time the case was created. This
// test logs the expected and obtained ratios, so that it is relatively easy for
// us to also check if we got any substantial improvements when working on delta
// improvements.
func TestDeltaSize(t *testing.T) {
	type recordedRatio struct {
		desc string
		want float64
		got  float64
	}

	allRatios := []recordedRatio{}

	testCases := []struct {
		base      string
		target    string
		wantRatio float64 // (targetSize / deltaSize) must be at least this much
	}{
		{
			base:      "image-001",
			target:    "image-002",
			wantRatio: 5.0,
		},
		{
			base:      "image-001",
			target:    "image-003",
			wantRatio: 2.5,
		},
		{
			base:      "image-004",
			target:    "image-005",
			wantRatio: 180.0,
		},
		{
			base:      "image-004",
			target:    "image-006",
			wantRatio: 4.0,
		},
	}

	client := testEnv.APIClient()
	ctx := context.Background()

	for _, tc := range testCases {
		desc := tc.base + "-" + tc.target
		t.Run(desc, func(t *testing.T) {
			delta := "delta-" + desc

			// Create delta
			rc, err := client.ImageDelta(ctx,
				tc.base,
				tc.target,
				types.ImageDeltaOptions{
					Tag: delta,
				})

			if err != nil {
				t.Fatalf("Error creating delta: %s", err)
			}
			io.Copy(ioutil.Discard, rc)
			rc.Close()

			// Check ratio
			gotRatio := queryDeltaRatio(ctx, t, client, tc.target, delta)
			if gotRatio < tc.wantRatio {
				t.Errorf("Delta ratio too small: got %.2f, expected at least %.2f",
					gotRatio, tc.wantRatio)
			}

			allRatios = append(allRatios, recordedRatio{desc, tc.wantRatio, gotRatio})
		})
	}

	// Log all obtained ratios
	t.Log("-------------------------------------------------------------")
	t.Logf("%-24s%-14s%-14s", "Test case", "Want ratio", "Got ratio")
	t.Log("-------------------------------------------------------------")
	for _, r := range allRatios {
		t.Logf("%-24s%-14.2f%-14.2f", r.desc, r.want, r.got)
	}
	t.Log("-------------------------------------------------------------")
}

func pullBaseAndTargetImages(t *testing.T, client apiclient.APIClient, base, target string) {
	var (
		err error
		rc  io.ReadCloser
		ctx = context.Background()
	)

	rc, err = client.ImagePull(ctx,
		base,
		types.ImagePullOptions{})
	assert.NilError(t, err)
	io.Copy(ioutil.Discard, rc)
	rc.Close()

	rc, err = client.ImagePull(ctx,
		target,
		types.ImagePullOptions{})
	assert.NilError(t, err)
	io.Copy(ioutil.Discard, rc)
	rc.Close()
}

// queryDeltaRatio queries image sizes and returns how many times target is
// larger than delta.
func queryDeltaRatio(ctx context.Context, t *testing.T, client apiclient.APIClient,
	target, delta string) float64 {
	targetSize := queryImageSize(ctx, t, client, target)
	deltaSize := queryImageSize(ctx, t, client, delta)
	deltaRatio := float64(targetSize) / float64(deltaSize)
	if targetSize == 0 {
		deltaRatio = 1.0
	}
	return deltaRatio
}

// queryImageSize returns the size in bytes of image.
func queryImageSize(ctx context.Context, t *testing.T, client apiclient.APIClient,
	image string) int64 {

	ii, _, err := client.ImageInspectWithRaw(ctx, image)
	if err != nil {
		t.Fatalf("Error inspecting image %q: %s", image, err)
	}
	return ii.Size
}
