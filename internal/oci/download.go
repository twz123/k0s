// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package oci

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/ptr"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// Download downloads the OCI artifact present at the given registry URL.
// Usage example:
//
// artifact := "docker.io/company/k0s:latest"
// fp, _ := os.CreateTemp("", "k0s-oci-artifact-*")
// err := oci.Download(ctx, artifact, fp)
//
// This function expects at least one artifact to be present, if none is found
// this returns an error. The artifact name can be specified using the
// WithArtifactName option.
func Download(ctx context.Context, url string, target io.Writer, options ...DownloadOption) (err error) {
	opts := downloadOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	creds, err := opts.auth.CredentialStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to create credential store: %w", err)
	}

	imgref, err := registry.ParseReference(url)
	if err != nil {
		return fmt.Errorf("failed to parse artifact reference: %w", err)
	}

	repo, err := remote.NewRepository(url)
	if err != nil {
		return fmt.Errorf("failed to create repository: %w", err)
	}

	if opts.plainHTTP {
		repo.PlainHTTP = true
	}

	transp := http.DefaultTransport.(*http.Transport).Clone()
	if opts.insecureSkipTLSVerify {
		transp.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	repo.Client = &auth.Client{
		Client:     &http.Client{Transport: transp},
		Credential: creds.Get,
	}

	tag := imgref.Reference
	successors, err := fetchSuccessors(ctx, repo, tag, ptr.To(platforms.DefaultSpec()))
	if err != nil {
		return fmt.Errorf("failed to fetch successors: %w", err)
	}

	source, err := findArtifactDescriptor(successors, opts)
	if err != nil {
		return fmt.Errorf("failed to find artifact: %w", err)
	}

	// Get a reader to the blob.
	raw, err := repo.Blobs().Fetch(ctx, source)
	if err != nil {
		return fmt.Errorf("failed to fetch blob: %w", err)
	}
	defer func() { err = errors.Join(err, raw.Close()) }()

	// Wrap the reader so that its length and digest will be verified.
	verified := content.NewVerifyReader(raw, source)

	// Copy over the blob to its target.
	if _, err := io.Copy(target, verified); err != nil {
		return fmt.Errorf("failed to copy blob: %w", err)
	}

	// Verify the digest.
	return verified.Verify()
}

// Fetches the manifest for the given reference and returns all of its successors.
func fetchSuccessors(ctx context.Context, repo oras.ReadOnlyTarget, reference string, targetPlatform *ocispec.Platform) ([]ocispec.Descriptor, error) {
	desc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve reference: %w", err)
	}

	switch desc.MediaType {
	case "application/vnd.docker.distribution.manifest.list.v2+json", ocispec.MediaTypeImageIndex:
		// For indexes, select the matching manifest. For plain manifests, don't
		// enforce target platform since artifact configs are often empty.
		resolved, err := oras.Resolve(ctx, repo, reference, oras.ResolveOptions{
			TargetPlatform: targetPlatform,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve reference: %w", err)
		}
		desc = resolved
	}

	return content.Successors(ctx, repo, desc)
}

// findArtifactDescriptor filters, out of the provided list of descriptors, the
// one that matches the given options. If no artifact name is provided, it
// returns the first descriptor.
func findArtifactDescriptor(all []ocispec.Descriptor, opts downloadOptions) (ocispec.Descriptor, error) {
	for _, desc := range all {
		if desc.MediaType == ocispec.MediaTypeEmptyJSON {
			continue
		}
		// if no artifact name is specified, we use the first one.
		fname := opts.artifactName
		if fname == "" || fname == desc.Annotations[ocispec.AnnotationTitle] {
			return desc, nil
		}
	}
	if opts.artifactName == "" {
		return ocispec.Descriptor{}, errors.New("no artifact descriptors found")
	}
	return ocispec.Descriptor{}, fmt.Errorf("artifact %q not found", opts.artifactName)
}

// downloadOptions holds the options used when downloading OCI artifacts.
type downloadOptions struct {
	insecureSkipTLSVerify bool
	auth                  DockerConfig
	artifactName          string
	plainHTTP             bool
}

// DownloadOption is a function that sets an option for the OCI download.
type DownloadOption func(*downloadOptions)

// WithInsecureSkipTLSVerify sets the insecureSkipTLSVerify option to true.
func WithInsecureSkipTLSVerify() DownloadOption {
	return func(opts *downloadOptions) {
		opts.insecureSkipTLSVerify = true
	}
}

// WithPlainHTTP sets the client to reach the remote registry using plain HTTP
// instead of HTTPS.
func WithPlainHTTP() DownloadOption {
	return func(opts *downloadOptions) {
		opts.plainHTTP = true
	}
}

// WithDockerAuth sets the Docker config to be used when authenticating to
// the registry.
func WithDockerAuth(auth DockerConfig) DownloadOption {
	return func(opts *downloadOptions) {
		opts.auth = auth
	}
}

// WithArtifactName sets the name of the artifact to be downloaded. This is
// used to filter out the artifacts present in the manifest.
func WithArtifactName(name string) DownloadOption {
	return func(opts *downloadOptions) {
		opts.artifactName = name
	}
}

// DockerConfigEntry holds an entry in the '.dockerconfigjson' file.
type DockerConfigEntry struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// DockerConfig represents the content of the '.dockerconfigjson' file.
type DockerConfig struct {
	Auths map[string]DockerConfigEntry `json:"auths"`
}

// CredentialStore turns the Docker configuration into a credential store and
// returns it.
func (d DockerConfig) CredentialStore(ctx context.Context) (credentials.Store, error) {
	creds := credentials.NewMemoryStore()
	for addr, entry := range d.Auths {
		if err := creds.Put(ctx, addr, auth.Credential{
			Username: entry.Username,
			Password: entry.Password,
		}); err != nil {
			return nil, fmt.Errorf("failed to add credential: %w", err)
		}
	}
	return creds, nil
}
