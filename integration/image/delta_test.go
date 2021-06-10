package image

import (
	"context"
	"io"
	"io/ioutil"
	"testing"

	"github.com/docker/docker/api/types"
	apiclient "github.com/docker/docker/client"
)

// TestDelta just creates a delta and checks if it exists
func TestDelta(t *testing.T) {
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
	if err != nil {
		t.Fatalf("Creating delta: %s", err)
	}
	io.Copy(ioutil.Discard, rc)
	rc.Close()

	_, _, err = client.ImageInspectWithRaw(ctx, delta)
	if err != nil {
		t.Fatalf("Inspecting delta: %s", err)
	}
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
	if err != nil {
		t.Fatalf("Pulling delta base: %s", err)
	}
	io.Copy(ioutil.Discard, rc)
	rc.Close()

	rc, err = client.ImagePull(ctx,
		target,
		types.ImagePullOptions{})
	if err != nil {
		t.Fatalf("Pulling delta target: %s", err)
	}
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
