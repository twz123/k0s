// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package oci_test

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/k0sproject/k0s/internal/oci"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/content"
	"sigs.k8s.io/yaml"
)

//go:embed testdata/*
var testData embed.FS

// we define our tests as yaml files inside the testdata directory. this
// function parses them and returns a map of the tests.
func parseTestsYAML[T any](t *testing.T) map[string]T {
	entries, err := testData.ReadDir("testdata")
	require.NoError(t, err)
	tests := make(map[string]T, 0)
	for _, entry := range entries {
		fpath := path.Join("testdata", entry.Name())
		data, err := testData.ReadFile(fpath)
		require.NoError(t, err)

		var onetest T
		err = yaml.Unmarshal(data, &onetest)
		require.NoError(t, err)

		tests[fpath] = onetest
	}
	return tests
}

// testFile represents a single test file inside the testdata directory.
type testFile struct {
	Manifest          string            `json:"manifest"`
	ManifestMediaType string            `json:"manifestMediaType"`
	Expected          string            `json:"expected"`
	Error             string            `json:"error"`
	Authenticated     bool              `json:"authenticated"`
	AuthUser          string            `json:"authUser"`
	AuthPass          string            `json:"authPass"`
	Artifacts         map[string]string `json:"artifacts"`
	ArtifactName      string            `json:"artifactName"`
	PlainHTTP         bool              `json:"plainHTTP"`
}

func TestDownload(t *testing.T) {
	for tname, tt := range parseTestsYAML[testFile](t) {
		t.Run(tname, func(t *testing.T) {
			addr := startOCIMockServer(t, tname, tt)

			opts := []oci.DownloadOption{oci.WithInsecureSkipTLSVerify()}
			if tt.Authenticated {
				entry := oci.DockerConfigEntry{tt.AuthUser, tt.AuthPass}
				opts = append(opts, oci.WithDockerAuth(
					oci.DockerConfig{
						Auths: map[string]oci.DockerConfigEntry{
							addr.Host: entry,
						},
					},
				))
			}

			if tt.ArtifactName != "" {
				opts = append(opts, oci.WithArtifactName(tt.ArtifactName))
			}

			if tt.PlainHTTP {
				opts = append(opts, oci.WithPlainHTTP())
			}

			buf := bytes.NewBuffer(nil)
			url := path.Join(addr.Host, "repository", "artifact:latest")
			err := oci.Download(t.Context(), url, buf, opts...)
			if tt.Expected != "" {
				require.NoError(t, err)
				require.Empty(t, tt.Error)
				require.Equal(t, tt.Expected, buf.String())
				return
			}
			require.NotEmpty(t, tt.Error)
			require.ErrorContains(t, err, tt.Error)
		})
	}
}

func TestDownloadFromIndex(t *testing.T) {
	t.Run("matches target platform", func(t *testing.T) {
		targetPlatform := platforms.DefaultSpec()
		otherArch := "arm64"
		if targetPlatform.Architecture == otherArch {
			otherArch = "amd64"
		}

		matchingManifestDesc, matchingManifest, matchingBlob := buildArtifactManifest(t, "matching")
		otherManifestDesc, otherManifest, otherBlob := buildArtifactManifest(t, "other")

		index := ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: []ocispec.Descriptor{
				{
					MediaType: ocispec.MediaTypeImageManifest,
					Digest:    matchingManifestDesc.Digest,
					Size:      matchingManifestDesc.Size,
					Platform: &ocispec.Platform{
						Architecture: targetPlatform.Architecture,
						OS:           targetPlatform.OS,
						OSVersion:    targetPlatform.OSVersion,
						OSFeatures:   targetPlatform.OSFeatures,
						Variant:      targetPlatform.Variant,
					},
				},
				{
					MediaType: ocispec.MediaTypeImageManifest,
					Digest:    otherManifestDesc.Digest,
					Size:      otherManifestDesc.Size,
					Platform: &ocispec.Platform{
						Architecture: otherArch,
						OS:           targetPlatform.OS,
						OSVersion:    targetPlatform.OSVersion,
						OSFeatures:   targetPlatform.OSFeatures,
						Variant:      targetPlatform.Variant,
					},
				},
			},
		}
		indexJSON, err := json.Marshal(index)
		require.NoError(t, err)

		server := startOCIIndexMockServer(t, ocispec.MediaTypeImageIndex, string(indexJSON), map[string]string{
			matchingManifestDesc.Digest.String(): string(matchingManifest),
			otherManifestDesc.Digest.String():    string(otherManifest),
		}, map[string]string{
			matchingBlob: "matching",
			otherBlob:    "other",
		})

		buf := bytes.NewBuffer(nil)
		url := path.Join(server.Host, "repository", "artifact:latest")
		err = oci.Download(t.Context(), url, buf, oci.WithInsecureSkipTLSVerify())
		require.NoError(t, err)
		require.Equal(t, "matching", buf.String())
	})

	t.Run("fails when index has no matching platform", func(t *testing.T) {
		targetPlatform := platforms.DefaultSpec()
		otherArch := "arm64"
		if targetPlatform.Architecture == otherArch {
			otherArch = "amd64"
		}

		manifestDesc, manifestJSON, blobDigest := buildArtifactManifest(t, "other")
		index := ocispec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			MediaType: ocispec.MediaTypeImageIndex,
			Manifests: []ocispec.Descriptor{
				{
					MediaType: ocispec.MediaTypeImageManifest,
					Digest:    manifestDesc.Digest,
					Size:      manifestDesc.Size,
					Platform: &ocispec.Platform{
						Architecture: otherArch,
						OS:           targetPlatform.OS,
						OSVersion:    targetPlatform.OSVersion,
						OSFeatures:   targetPlatform.OSFeatures,
						Variant:      targetPlatform.Variant,
					},
				},
			},
		}
		indexJSON, err := json.Marshal(index)
		require.NoError(t, err)

		server := startOCIIndexMockServer(t, ocispec.MediaTypeImageIndex, string(indexJSON), map[string]string{
			manifestDesc.Digest.String(): string(manifestJSON),
		}, map[string]string{
			blobDigest: "other",
		})

		buf := bytes.NewBuffer(nil)
		url := path.Join(server.Host, "repository", "artifact:latest")
		err = oci.Download(t.Context(), url, buf, oci.WithInsecureSkipTLSVerify())
		require.ErrorContains(t, err, "no matching manifest was found in the manifest list")
	})
}

// startOCIMockServer starts a mock server that will respond to the given test.
// this mimics the behavior of the real OCI registry. This function returns the
// address of the server.
func startOCIMockServer(t *testing.T, tname string, test testFile) url.URL {
	var serverURL *url.URL

	starter := httptest.NewTLSServer
	if test.PlainHTTP {
		starter = httptest.NewServer
	}

	server := starter(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Log(r.Proto, r.Method, r.RequestURI)
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			// this is a request to authenticate.
			if strings.Contains(r.URL.Path, "/token") {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				user, pass, _ := r.BasicAuth()
				if user != "user" || pass != "pass" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				res := map[string]string{"token": tname}
				marshaled, err := json.Marshal(res)
				assert.NoError(t, err)
				_, _ = w.Write(marshaled)
				return
			}

			// verify if the request should be authenticated or
			// not. if it has already been authenticated then just
			// moves on. the token returned is the test name.
			tokenhdr, authenticated := r.Header["Authorization"]
			if !authenticated && test.Authenticated {
				proto := "https"
				if test.PlainHTTP {
					proto = "http"
				}

				header := fmt.Sprintf(`Bearer realm="%s://%s/token"`, proto, serverURL.Host)
				w.Header().Add("WWW-Authenticate", header)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// verify if the token provided by the client matches
			// the expected token.
			if test.Authenticated {
				assert.Len(t, tokenhdr, 1)
				assert.Contains(t, tokenhdr[0], tname)
			}

			// serve the manifest.
			if strings.Contains(r.URL.Path, "/manifests/") {
				ref := path.Base(r.URL.Path)
				if _, digest, ok := strings.Cut(ref, "sha256:"); ok && digest != "" {
					if _, ok := test.Artifacts[digest]; ok {
						assert.Failf(t, "unexpected request", "blob digest fetched via manifests API: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
				}
				manifestMediaType := cmp.Or(test.ManifestMediaType, "application/vnd.oci.image.manifest.v1+json")
				manifestDigest := content.NewDescriptorFromBytes(manifestMediaType, []byte(test.Manifest)).Digest.String()
				w.Header().Add("Content-Type", manifestMediaType)
				w.Header().Add("Docker-Content-Digest", manifestDigest)
				w.Header().Add("Content-Length", strconv.Itoa(len(test.Manifest)))
				if r.Method == http.MethodHead {
					return
				}
				_, _ = w.Write([]byte(test.Manifest))
				return
			}

			// serve a layer or the config blob.
			if strings.Contains(r.URL.Path, "/blobs/") {
				ref := path.Base(r.URL.Path)
				if _, digest, ok := strings.Cut(ref, "sha256:"); !ok || digest == "" {
					w.WriteHeader(http.StatusNotFound)
					return
				} else if content, ok := test.Artifacts[digest]; ok {
					w.Header().Add("Content-Length", strconv.Itoa(len(content)))
					if r.Method == http.MethodHead {
						return
					}
					_, _ = w.Write([]byte(content))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}

			assert.Failf(t, "unexpected request", "%s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	return *serverURL
}

func startOCIIndexMockServer(t *testing.T, rootMediaType, rootManifest string, manifestsByDigest map[string]string, blobsByDigest map[string]string) url.URL {
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Log(r.Proto, r.Method, r.RequestURI)
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			if strings.Contains(r.URL.Path, "/manifests/") {
				ref := path.Base(r.URL.Path)
				if ref == "latest" {
					manifestDigest := content.NewDescriptorFromBytes(rootMediaType, []byte(rootManifest)).Digest.String()
					w.Header().Add("Content-Type", rootMediaType)
					w.Header().Add("Docker-Content-Digest", manifestDigest)
					w.Header().Add("Content-Length", strconv.Itoa(len(rootManifest)))
					if r.Method == http.MethodHead {
						return
					}
					_, _ = w.Write([]byte(rootManifest))
					return
				}
				if manifest, ok := manifestsByDigest[ref]; ok {
					manifestDigest := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, []byte(manifest)).Digest.String()
					w.Header().Add("Content-Type", ocispec.MediaTypeImageManifest)
					w.Header().Add("Docker-Content-Digest", manifestDigest)
					w.Header().Add("Content-Length", strconv.Itoa(len(manifest)))
					if r.Method == http.MethodHead {
						return
					}
					_, _ = w.Write([]byte(manifest))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if strings.Contains(r.URL.Path, "/blobs/") {
				ref := path.Base(r.URL.Path)
				if _, digest, ok := strings.Cut(ref, "sha256:"); !ok || digest == "" {
					w.WriteHeader(http.StatusNotFound)
					return
				} else if blob, ok := blobsByDigest[digest]; ok {
					w.Header().Add("Content-Length", strconv.Itoa(len(blob)))
					if r.Method == http.MethodHead {
						return
					}
					_, _ = w.Write([]byte(blob))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}

			assert.Failf(t, "unexpected request", "%s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	return *serverURL
}

func buildArtifactManifest(t *testing.T, payload string) (ocispec.Descriptor, []byte, string) {
	layer := content.NewDescriptorFromBytes("application/vnd.oci.image.layer.v1.tar", []byte(payload))
	layer.Annotations = map[string]string{
		ocispec.AnnotationTitle: "artifact",
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: "application/vnd.unknown.artifact.v1",
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{layer},
	}

	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	manifestDesc := content.NewDescriptorFromBytes(ocispec.MediaTypeImageManifest, manifestJSON)
	return manifestDesc, manifestJSON, strings.TrimPrefix(layer.Digest.String(), "sha256:")
}
