package image

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
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

	if err := pullImages(client, []string{base, target}); err != nil {
		t.Fatal(err)
	}

	rc, err = client.ImageDelta(ctx,
		base,
		target,
		types.ImageDeltaOptions{
			Tag: delta,
		})
	if err != nil {
		t.Fatalf("Creating delta: %s", err)
	}
	// io.Copy(ioutil.Discard, rc)
	io.Copy(os.Stdout, rc)
	rc.Close()

	_, _, err = client.ImageInspectWithRaw(ctx, delta)
	if err != nil {
		t.Fatalf("Inspecting delta: %s", err)
	}
}

func pullImages(client apiclient.APIClient, images []string) error {
	var (
		err error
		rc  io.ReadCloser
		ctx = context.Background()
	)

	for _, image := range images {
		rc, err = client.ImagePull(ctx,
			image,
			types.ImagePullOptions{
				All:           false,
				RegistryAuth:  "",
				PrivilegeFunc: nil,
				Platform:      "",
			})
		if err != nil {
			return fmt.Errorf("Failed to pull image %q: %s", image, err)
		}
		io.Copy(ioutil.Discard, rc)
		rc.Close()
	}

	return nil
}
