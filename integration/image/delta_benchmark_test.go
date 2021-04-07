package image

import (
	"context"
	"testing"
)

func BenchmarkDelta(b *testing.B) {
	var (
		base    = "balena/open-balena-base:11.1.1"
		target1 = "balena/open-balena-api:0.134.0"
		delta1  = "delta-base-api"
		target2 = "balena/open-balena-vpn:9.17.9"
		delta2  = "delta-base-vpn"
	)

	var (
		ctx    = context.Background()
		client = testEnv.APIClient()
	)

	if err := pullImages(client, []string{base, target1, target2}); err != nil {
		b.Fatal(err)
	}

	var (
		baseSize    int64
		target1Size int64
		target2Size int64
	)
	{
		res, _, err := client.ImageInspectWithRaw(ctx, base)
		if err != nil {
			b.Fatalf("Inspecting image %q: %s", base, err)
		}
		baseSize = res.Size
	}
	{
		res, _, err := client.ImageInspectWithRaw(ctx, target1)
		if err != nil {
			b.Fatalf("Inspecting image %q: %s", target1, err)
		}
		target1Size = res.Size
	}
	{
		res, _, err := client.ImageInspectWithRaw(ctx, target2)
		if err != nil {
			b.Fatalf("Inspecting image %q: %s", target2, err)
		}
		target2Size = res.Size
	}

	b.Run("BaseToTarget1", func(b *testing.B) {
		b.SetBytes(baseSize + target1Size)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := doDelta(client, base, target1, delta1); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		var deltaSize int64
		{
			res, _, err := client.ImageInspectWithRaw(ctx, delta1)
			if err != nil {
				b.Fatalf("Inspecting delta: %s", err)
			}
			deltaSize = res.Size
		}
		b.Logf("base   size:    %v bytes", baseSize)
		b.Logf("target size:    %v bytes", target1Size)
		b.Logf("delta  size:    %v bytes, %.2fx improvement", deltaSize, (float64(target1Size) / float64(deltaSize)))
	})

	b.Run("BaseToTarget2", func(b *testing.B) {
		b.SetBytes(baseSize + target2Size)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := doDelta(client, base, target2, delta2); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		var deltaSize int64
		{
			res, _, err := client.ImageInspectWithRaw(ctx, delta2)
			if err != nil {
				b.Fatalf("Inspecting delta: %s", err)
			}
			deltaSize = res.Size
		}
		b.Logf("base   size:    %v bytes", baseSize)
		b.Logf("target size:    %v bytes", target2Size)
		b.Logf("delta  size:    %v bytes, %.2fx improvement", deltaSize, (float64(target2Size) / float64(deltaSize)))
	})
}
