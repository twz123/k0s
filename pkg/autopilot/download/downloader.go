// Copyright 2021 k0s authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"path/filepath"
	"strings"

	internalhttp "github.com/k0sproject/k0s/internal/http"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/sirupsen/logrus"
)

type Downloader interface {
	Download(ctx context.Context) error
}

type Config struct {
	URL string

	// Hex-encoded hash, optionally prefixed with the algorithm and a colon.
	// Defaults to SHA-256 if no algorithm prefix is found.
	ExpectedHash string

	DownloadDir string
}

type downloader struct {
	config Config
	logger logrus.FieldLogger
}

var _ Downloader = (*downloader)(nil)

func NewDownloader(config Config, logger logrus.FieldLogger) Downloader {
	return &downloader{
		config: config,
		logger: logger.WithField("component", "downloader"),
	}
}

// Start begins the download process, starting the downloading functionality
// on a separate goroutine. Cancelling the context will abort this operation
// once started.
func (d *downloader) Download(ctx context.Context) (err error) {
	// Create a hash instance based on the provided hashType.
	// Defaults to SHA-256 if no hash type is given.
	var hasher hash.Hash
	encodedHash := d.config.ExpectedHash
	if hashType, hashVal, typed := strings.Cut(encodedHash, ":"); typed {
		encodedHash = hashVal
		switch hashType {
		case "sha256":
			hasher = sha256.New()
		case "sha512":
			hasher = sha512.New()
		default:
			return fmt.Errorf("unsupported hash type: %q", hashType)
		}
	} else {
		hasher = sha256.New()
	}
	expectedHash, err := hex.DecodeString(encodedHash)
	if err != nil {
		return fmt.Errorf("hash is not a hexadecimal encoded string: %w", err)
	}

	// Set up target file for download.
	target, err := file.AtomicWithTarget(filepath.Join(d.config.DownloadDir, "download")).Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, target.Close()) }()

	// Download from URL into target and take the hash.
	var baseName string
	if err = internalhttp.Download(ctx, d.config.URL, &baseName, io.MultiWriter(hasher, target)); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Check the downloaded hash and abort if it doesn't match.
	if downloadedHash := hasher.Sum(nil); !bytes.Equal(expectedHash, downloadedHash) {
		return fmt.Errorf("hash mismatch: expected %x, got %x", expectedHash, downloadedHash)
	}

	// All is well. Finish the download.
	if err := target.FinishWithBaseName(baseName); err != nil {
		return fmt.Errorf("failed to finish download: %w", err)
	}

	return nil
}
