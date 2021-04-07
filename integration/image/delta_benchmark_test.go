package image

import (
	"context"
	"io"
	"io/ioutil"
	"testing"

	"github.com/docker/docker/api/types"
)

func BenchmarkDelta(b *testing.B) {
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
		b.Fatal(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rc, err = client.ImageDelta(ctx,
			base,
			target,
			types.ImageDeltaOptions{
				Tag: delta,
			})
		if err != nil {
			b.Fatalf("Creating delta: %s", err)
		}
		io.Copy(ioutil.Discard, rc)
		// io.Copy(os.Stdout, rc)
		rc.Close()
	}
}
