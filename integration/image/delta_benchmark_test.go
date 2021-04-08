package image

import (
	"context"
	"testing"
)

func BenchmarkDelta(b *testing.B) {
	var (
		base   = "balena/open-balena-base:11.1.1"
		target = "balena/open-balena-api:0.134.0"
		delta  = "delta-base-api"
	)

	var (
		ctx    = context.Background()
		client = testEnv.APIClient()
	)

	if err := pullImages(client, []string{base, target, "hello-world:latest"}); err != nil {
		b.Fatal(err)
	}

	var (
		baseSize   int64
		targetSize int64
	)
	{
		res, _, err := client.ImageInspectWithRaw(ctx, base)
		if err != nil {
			b.Fatalf("Inspecting image %q: %s", base, err)
		}
		baseSize = res.Size
	}
	{
		res, _, err := client.ImageInspectWithRaw(ctx, target)
		if err != nil {
			b.Fatalf("Inspecting image %q: %s", target, err)
		}
		targetSize = res.Size
	}

	// warm cache, generates the signature for base
	if err := doDelta(client, base, "hello-world:latest", "delta-warm-cache"); err != nil {
		b.Fatal(err)
	}

	b.SetBytes(baseSize + targetSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := doDelta(client, base, target, delta); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	var deltaSize int64
	{
		res, _, err := client.ImageInspectWithRaw(ctx, delta)
		if err != nil {
			b.Fatalf("Inspecting delta: %s", err)
		}
		deltaSize = res.Size
	}
	b.Logf("base   size:    %v bytes", baseSize)
	b.Logf("target size:    %v bytes", targetSize)
	b.Logf("delta  size:    %v bytes, %.2fx improvement", deltaSize, (float64(targetSize) / float64(deltaSize)))
}
