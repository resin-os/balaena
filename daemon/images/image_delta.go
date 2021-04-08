package images

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/balena-os/librsync-go"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/image"
	"github.com/docker/docker/layer"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/progress"
	"github.com/docker/docker/pkg/streamformatter"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/go-units"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// DeltaCreate creates a delta of the specified src and dest images
// This is called directly from the Engine API
func (i *ImageService) DeltaCreate(deltaSrc, deltaDest string, options types.ImageDeltaOptions, outStream io.Writer) error {
	progressOutput := streamformatter.NewJSONProgressOutput(outStream, false)

	srcImg, err := i.GetImage(deltaSrc)
	if err != nil {
		return errors.Wrapf(err, "no such image: %s", deltaSrc)
	}

	dstImg, err := i.GetImage(deltaDest)
	if err != nil {
		return errors.Wrapf(err, "no such image: %s", deltaDest)
	}

	is := i.ImageStore()
	ls := i.LayerStore(dstImg.OperatingSystem())

	srcData, err := is.GetTarSeekStream(srcImg.ID())
	if err != nil {
		return err
	}
	defer srcData.Close()

	srcDataLen, err := ioutils.SeekerSize(srcData)
	if err != nil {
		return err
	}

	progressReader := progress.NewProgressReader(srcData, progressOutput, srcDataLen, deltaSrc, "Fingerprinting")
	defer progressReader.Close()

	srcSig, err := librsync.Signature(bufio.NewReaderSize(progressReader, 65536), ioutil.Discard, 512, 32, librsync.BLAKE2_SIG_MAGIC)
	if err != nil {
		return err
	}

	progress.Update(progressOutput, deltaSrc, "Fingerprint complete")

	deltaRootFS := image.NewRootFS()

	for _, diffID := range dstImg.RootFS.DiffIDs {
		progress.Update(progressOutput, stringid.TruncateID(diffID.String()), "Waiting")
	}

	statTotalSize := int64(0)
	statDeltaSize := int64(0)

	for i, diffID := range dstImg.RootFS.DiffIDs {
		var (
			layerData io.Reader
		)

		commonLayer := false
		dstRootFS := *dstImg.RootFS
		dstRootFS.DiffIDs = dstRootFS.DiffIDs[:i+1]

		if i < len(srcImg.RootFS.DiffIDs) {
			srcRootFS := *srcImg.RootFS
			srcRootFS.DiffIDs = srcRootFS.DiffIDs[:i+1]

			if srcRootFS.ChainID() == dstRootFS.ChainID() {
				commonLayer = true
			}
		}

		// We're only interested in layers that are different. Push empty
		// layers for common layers
		if commonLayer {
			layerData, _ = layer.EmptyLayer.TarStream()
		} else {

			l, err := ls.Get(dstRootFS.ChainID())
			if err != nil {
				return err
			}
			defer layer.ReleaseAndLog(ls, l)

			input, err := l.TarStream()
			if err != nil {
				return err
			}
			defer input.Close()

			inputSize, err := l.DiffSize()
			if err != nil {
				return err
			}

			statTotalSize += inputSize

			progressReader := progress.NewProgressReader(input, progressOutput, inputSize, stringid.TruncateID(diffID.String()), "Computing delta")
			defer progressReader.Close()

			pR, pW := io.Pipe()

			layerData = pR

			tmpDelta, err := ioutil.TempFile("", "docker-delta-")
			if err != nil {
				return err
			}
			defer os.Remove(tmpDelta.Name())

			go func() {
				w := bufio.NewWriter(tmpDelta)
				err := librsync.Delta(srcSig, bufio.NewReader(progressReader), w)
				if err != nil {
					pW.CloseWithError(err)
					return
				}
				w.Flush()

				info, err := tmpDelta.Stat()
				if err != nil {
					pW.CloseWithError(err)
					return
				}

				tw := tar.NewWriter(pW)

				hdr := &tar.Header{
					Name: "delta",
					Mode: 0600,
					Size: info.Size(),
				}

				if err := tw.WriteHeader(hdr); err != nil {
					pW.CloseWithError(err)
					return
				}

				if _, err := tmpDelta.Seek(0, io.SeekStart); err != nil {
					pW.CloseWithError(err)
					return
				}

				if _, err := io.Copy(tw, tmpDelta); err != nil {
					pW.CloseWithError(err)
					return
				}

				if err := tw.Close(); err != nil {
					pW.CloseWithError(err)
					return
				}

				pW.Close()
			}()
		}

		newLayer, err := ls.Register(layerData, deltaRootFS.ChainID())
		if err != nil {
			return err
		}
		defer layer.ReleaseAndLog(ls, newLayer)

		if commonLayer {
			progress.Update(progressOutput, stringid.TruncateID(diffID.String()), "Skipping common layer")
		} else {
			deltaSize, err := newLayer.DiffSize()
			if err != nil {
				return err
			}
			statDeltaSize += deltaSize
			progress.Update(progressOutput, stringid.TruncateID(diffID.String()), "Delta complete")
		}

		deltaRootFS.Append(newLayer.DiffID())
	}

	config := image.Image{
		RootFS: deltaRootFS,
		V1Image: image.V1Image{
			Created: time.Now().UTC(),
			Config: &containertypes.Config{
				Labels: map[string]string{
					"io.resin.delta.base":   srcImg.ID().String(),
					"io.resin.delta.config": string(dstImg.RawJSON()),
				},
			},
		},
	}

	rawConfig, err := json.Marshal(config)
	if err != nil {
		return err
	}

	id, err := is.Create(rawConfig)
	if err != nil {
		return err
	}

	humanTotal := units.HumanSize(float64(statTotalSize))
	humanDelta := units.HumanSize(float64(statDeltaSize))
	deltaRatio := float64(statTotalSize) / float64(statDeltaSize)
	if statTotalSize == 0 {
		deltaRatio = 1
	}

	outStream.Write(streamformatter.FormatStatus("", "Normal size: %s, Delta size: %s, %.2fx improvement", humanTotal, humanDelta, deltaRatio))
	outStream.Write(streamformatter.FormatStatus("", "Created delta: %s", id.String()))

	if options.Tag == "" {
		return nil
	}

	ref, err := reference.ParseNormalizedNamed(options.Tag)
	if err != nil {
		return err
	}

	if _, isCanonical := ref.(reference.Canonical); isCanonical {
		return errors.New("build tag cannot contain a digest")
	}

	ref = reference.TagNameOnly(ref)

	if err := i.TagImageWithReference(id, ref); err != nil {
		return err
	}
	logrus.Debugf("Tagged delta %s with %s", id.String(), reference.FamiliarString(ref))
	outStream.Write(streamformatter.FormatStatus("", "Successfully tagged %s\n", reference.FamiliarString(ref)))

	return nil
}
