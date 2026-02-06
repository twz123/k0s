//go:build hack

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/distribution/reference"
	internalio "github.com/k0sproject/k0s/internal/io"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	imagespecs "github.com/opencontainers/image-spec/specs-go"
	imagespecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

func main() {
	out := bufio.NewWriter(os.Stdout)
	err := run(out, os.Args[1:])
	if err := errors.Join(err, out.Flush()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(out io.Writer, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	imageRef, err := reference.Parse(args[0])
	if err != nil {
		return fmt.Errorf("%w: %s", err, args[0])
	}

	specs, err := parseImageSpecs(args[1:])
	if err != nil {
		return err
	}

	return writeOCIArchive(ctx, out, imageRef, specs)
}

type imageSpec struct {
	platform  imagespecv1.Platform
	filePaths []string
}

func parseImageSpecs(args []string) (specs []imageSpec, _ error) {
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			continue
		}

		platform, err := platforms.Parse(args[0])
		if err != nil {
			return nil, err
		}

		filePaths := args[1:]
		if nextPlatform := slices.Index(args, "--"); nextPlatform >= 0 {
			filePaths = args[1:nextPlatform]
			args = args[nextPlatform+1:]
		} else {
			args = nil
		}

		if len(filePaths) > 1 {
			slices.SortFunc(filePaths, func(l, r string) int {
				return strings.Compare(filepath.Base(l), filepath.Base(r))
			})

			for i, filePath := range filePaths[1:] {
				l, r := filePaths[i], filePath
				if filepath.Base(l) == filepath.Base(r) {
					return nil, fmt.Errorf("duplicate file name for paths %q and %q", l, r)
				}
			}
		}

		specs = append(specs, imageSpec{platform, filePaths})
	}

	return specs, nil
}

type compressedFile struct {
	name             string
	compressedData   []byte
	uncompressedSize uint64
	desc             imagespecv1.Descriptor
}

func writeOCIArchive(ctx context.Context, w io.Writer, imageRef reference.Reference, specs []imageSpec) (err error) {
	imageAnnotations := map[string]string{}
	if sourceDateEpoch := os.Getenv("SOURCE_DATE_EPOCH"); sourceDateEpoch != "" {
		secs, err := strconv.ParseUint(sourceDateEpoch, 10, 64)
		if err != nil {
			return err
		}
		imageAnnotations[imagespecv1.AnnotationCreated] = time.Unix(int64(secs), 0).Format(time.RFC3339)
	}

	ociArchive := ociArchiveWriter{tar: tar.NewWriter(w)}
	var manifests []imagespecv1.Descriptor

	for _, spec := range specs {
		compressedFiles := make([]<-chan *compressedFile, len(spec.filePaths))
		g, ctx := errgroup.WithContext(ctx)
		for i, filePath := range spec.filePaths {
			compressedFile := make(chan *compressedFile, 1)
			compressedFiles[i] = compressedFile
			g.Go(func() error {
				defer close(compressedFile)
				f, err := compressFile(ctx, filePath)
				if err != nil {
					return fmt.Errorf("while compressing %s: %w", filePath, err)
				}

				fmt.Fprintf(os.Stderr, "Compressed %s: %.1f MiB -> %.1f MiB (%.0f%%)\n",
					f.name,
					float64(f.uncompressedSize)/1024/1024,
					float64(f.desc.Size)/1024/1024,
					100*(1-(float64(f.desc.Size)/float64(f.uncompressedSize))),
				)
				compressedFile <- f
				return nil
			})
		}

		var layers []imagespecv1.Descriptor
		for _, f := range compressedFiles {
			f := <-f
			if f == nil {
				return fmt.Errorf("failed to compress: %w", g.Wait())
			}
			if err := ociArchive.writeBlob(f.desc, f.compressedData); err != nil {
				return fmt.Errorf("failed to write layer for %s: %w: %+#v", f.name, err, f.desc)
			}
			fmt.Fprintf(os.Stderr, "Written blob for %s for %s: %s\n", f.name, platforms.Format(spec.platform), f.desc.Digest)
			layers = append(layers, f.desc)
		}

		configData, err := json.Marshal(spec.platform)
		if err != nil {
			return err
		}

		manifest := imagespecv1.Manifest{
			Versioned:    imagespecs.Versioned{SchemaVersion: 2},
			MediaType:    imagespecv1.MediaTypeImageManifest,
			ArtifactType: imagespecv1.MediaTypeImageConfig,
			Config: imagespecv1.Descriptor{
				MediaType: imagespecv1.MediaTypeImageConfig,
				Digest:    digest.Canonical.FromBytes(configData),
				Size:      int64(len(configData)),
				Data:      configData,
			},
			Layers:      layers,
			Annotations: imageAnnotations,
		}

		manifestData, err := json.Marshal(&manifest)
		if err != nil {
			return err
		}

		manifestDesc := imagespecv1.Descriptor{
			MediaType:    manifest.MediaType,
			ArtifactType: manifest.ArtifactType,
			Digest:       digest.FromBytes(manifestData),
			Size:         int64(len(manifestData)),
			//	Annotations: imageAnnotations,
			Platform: &spec.platform,
		}

		if err := ociArchive.writeBlob(manifest.Config, configData); err != nil {
			return fmt.Errorf("failed to write config: %w", err)
		}
		if err := ociArchive.writeBlob(manifestDesc, manifestData); err != nil {
			return fmt.Errorf("failed to write manifest: %w", err)
		}

		manifests = append(manifests, manifestDesc)
	}

	multiArchImage := imagespecv1.Index{
		Versioned: imagespecs.Versioned{SchemaVersion: 2},
		MediaType: imagespecv1.MediaTypeImageIndex,
		Manifests: manifests,
	}
	multiArchImageData, err := json.Marshal(&multiArchImage)
	if err != nil {
		return err
	}
	multiArchImageDesc := imagespecv1.Descriptor{
		MediaType: multiArchImage.MediaType,
		Digest:    digest.FromBytes(multiArchImageData),
		Size:      int64(len(multiArchImageData)),
		Annotations: map[string]string{
			imagespecv1.AnnotationRefName: imageRef.String(),
		},
	}
	if err := ociArchive.writeBlob(multiArchImageDesc, multiArchImageData); err != nil {
		return fmt.Errorf("failed to write multi-arch image index: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Written multi-arch image manifest:", multiArchImageDesc.Digest)

	return ociArchive.finalize(imagespecv1.Index{
		Versioned: imagespecs.Versioned{SchemaVersion: 2},
		MediaType: imagespecv1.MediaTypeImageIndex,
		Manifests: []imagespecv1.Descriptor{multiArchImageDesc},
	})
}

func compressFile(ctx context.Context, filePath string) (*compressedFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()

	var buf bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}

	digester := digest.Canonical.Digester()
	hash := digester.Hash()
	writeMonitor := internalio.WriterFunc(func(p []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return hash.Write(p)
	})

	bytesCopied, err := io.Copy(io.MultiWriter(writeMonitor, compressed), file)
	if err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}

	name, data := filepath.Base(filePath), buf.Bytes()
	return &compressedFile{
		name:             name,
		compressedData:   data,
		uncompressedSize: uint64(bytesCopied),
		desc: imagespecv1.Descriptor{
			MediaType: imagespecv1.MediaTypeImageLayerGzip,
			Size:      int64(len(data)),
			Digest:    digest.FromBytes(data),
			Annotations: map[string]string{
				imagespecv1.AnnotationTitle:                  name,
				"io.k0sproject.artifact.name":                name,
				"io.k0sproject.artifact.uncompressed.size":   strconv.FormatInt(bytesCopied, 10),
				"io.k0sproject.artifact.uncompressed.digest": digester.Digest().String(),
			},
		},
	}, nil
}

type ociArchiveWriter struct {
	tar   *tar.Writer
	blobs []digest.Digest
}

func (w *ociArchiveWriter) finalize(index imagespecv1.Index) (err error) {
	defer func() { err = errors.Join(err, w.tar.Close()) }()

	if err := writeTarJSON(w.tar, imagespecv1.ImageIndexFile, 0644, index); err != nil {
		return err
	}

	return writeTarJSON(w.tar, imagespecv1.ImageLayoutFile, 0444, &imagespecv1.ImageLayout{
		Version: imagespecv1.ImageLayoutVersion,
	})
}

func (w *ociArchiveWriter) Close() error {
	return w.tar.Close()
}

func (w *ociArchiveWriter) writeBlob(expected imagespecv1.Descriptor, data []byte) (err error) {
	d := expected.Digest
	if err := d.Validate(); err != nil {
		return err
	}

	idx, exists := slices.BinarySearch(w.blobs, d)
	if exists {
		return nil
	}

	if len(w.blobs) < 1 {
		if err := writeTarDir(w.tar, imagespecv1.ImageBlobsDir); err != nil {
			return err
		}
	}

	if (idx == 0 || w.blobs[idx-1].Algorithm() != d.Algorithm()) &&
		(idx >= len(w.blobs) || w.blobs[idx].Algorithm() != d.Algorithm()) {
		dirName := path.Join(imagespecv1.ImageBlobsDir, d.Algorithm().String())
		if err := writeTarDir(w.tar, dirName); err != nil {
			return err
		}
	}

	blobName := path.Join(imagespecv1.ImageBlobsDir, d.Algorithm().String(), d.Hex())
	verify := content.NewVerifyReader(bytes.NewReader(data), expected)
	if err := writeTarFile(w.tar, blobName, 0444, expected.Size, verify); err != nil {
		return err
	}
	if err := verify.Verify(); err != nil {
		return fmt.Errorf("failed to verify: %w", err)
	}

	w.blobs = slices.Insert(w.blobs, idx, d)
	return nil
}

func writeTarDir(w *tar.Writer, name string) error {
	return w.WriteHeader(&tar.Header{
		Name:     name + "/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})
}

func writeTarJSON(w *tar.Writer, name string, mode os.FileMode, data any) error {
	json, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return writeTarFile(w, name, mode, int64(len(json)), bytes.NewReader(json))
}

func writeTarFile(w *tar.Writer, name string, mode os.FileMode, size int64, in io.Reader) error {
	if err := w.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     int64(mode),
		Size:     size,
	}); err != nil {
		return err
	}

	_, err := io.Copy(w, in)
	return err
}
