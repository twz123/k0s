/*
Copyright 2022 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package file_test

import (
	"net/url"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/file"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPOSIXPathToURL(t *testing.T) {
	tests := []struct {
		name, path string
		expected   urlAsserter
	}{
		{"empty", "", notAbsErr{"", "no leading slash"}},
		{"root", "/", localPath("/")},
		{"root_path", "/foo", localPath("/foo")},
		{"root_nested_path", "/path/to/file", localPath("/path/to/file")},
		{"relative_path", "foo", notAbsErr{"foo", "no leading slash"}},
		{"relative_nested_path", "path/to/file", notAbsErr{"path/to/file", "no leading slash"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u, err := file.POSIXPathToURL(test.path)
			test.expected.assertURL(t, u, err)
		})
	}
}

func TestURLToPOSIXPath(t *testing.T) {
	URL := func(s string) *url.URL { return parseURL(t, s) }

	// url.URL cannot distinguish between an empty authority and path and a
	// missing authority, i.e. "file:" and "file://" produce an identical
	// URL whose string representation is "file:". In Theory, the error
	// messages should be different for both cases. The "file:" case should
	// produce a "no leading slash" error, while the "file://" case should
	// produce an "invalid URL" error.
	emptyURLNoAuthority, emptyURL := URL("file:"), URL("file://")
	require.Equal(t, emptyURLNoAuthority, emptyURL, "Go changed url.Parse()?")
	require.False(t, emptyURLNoAuthority.OmitHost, "Go changed url.Parse()?")
	emptyURLNoAuthority.OmitHost = true
	require.Equal(t, "file:", emptyURLNoAuthority.String(), "Go changed url.Parse()?")

	tests := []struct {
		name     string
		url      *url.URL
		expected pathAsserter
	}{
		{"non_file_url", URL("http://foo/bar"),
			errorMessage("not a file URL")},
		{"opaque_file_url", URL("file::"),
			errorMessage("file URL is opaque")},

		{"empty", emptyURLNoAuthority,
			notAbsErr{"", "no leading slash"}},
		{"root", URL("file:/"),
			localPath("/")},
		{"root_path", URL("file:/foo"),
			localPath("/foo")},
		{"root_nested_path", URL("file:/path/to/file"),
			localPath("/path/to/file")},

		{"escaped_slash", URL("file:/path/to%2ffile"),
			errorMessage("path must not include encoded path separators")},
		{"escaped_backslash", URL("file:/path/to%5cfile"),
			localPath("/path/to\\file")},

		{"relative_path", URL("file:foo"),
			errorMessage("file URL is opaque")},
		{"relative_nested_path", URL("file:path/to/file"),
			errorMessage("file URL is opaque")},
		{"relative_path_url", &url.URL{Scheme: "file", OmitHost: true, Path: "foo"},
			notAbsErr{"foo", "no leading slash"}},
		{"relative_nested_path_url", &url.URL{Scheme: "file", OmitHost: true, Path: "path/to/file"},
			notAbsErr{"path/to/file", "no leading slash"}},

		{"authority_empty", emptyURL,
			notAbsErr{"", "no leading slash"}},
		{"authority_root", URL("file:///"),
			localPath("/")},
		{"authority_root_path", URL("file:///foo"),
			localPath("/foo")},
		{"authority_root_nested_path", URL("file:///path/to/file"),
			localPath("/path/to/file")},

		{"authority_relative_path", URL("file://foo"), // this will be {Host:"foo",Path:""}
			errorMessage("path is non-local")},
		{"authority_relative_nested_path", URL("file://path/to/file"), // this will be {Host:"path",Path:"/to/file"}
			errorMessage("path is non-local")},
		// The URLs in the two tests below don't have a valid string representation
		{"authority_relative_path_url", &url.URL{Scheme: "file", Path: "foo"},
			errorMessage("invalid URL: no leading slash in path: foo")},
		{"authority_relative_nested_path_url", &url.URL{Scheme: "file", Path: "path/to/file"},
			errorMessage("invalid URL: no leading slash in path: path/to/file")},

		{"localhost_empty", URL("file://localhost"),
			notAbsErr{"", "no leading slash"}},
		{"localhost_root", URL("file://localhost/"),
			localPath("/")},
		{"localhost_path", URL("file://localhost/foo"),
			localPath("/foo")},
		{"localhost_nested_path", URL("file://localhost/path/to/file"),
			localPath("/path/to/file")},
		// The URLs in the two tests below don't have a valid string representation
		{"localhost_relative_path", &url.URL{Scheme: "file", Host: "localhost", Path: "foo"},
			errorMessage("invalid URL: no leading slash in path: foo")},
		{"localhost_relative_nested_path", &url.URL{Scheme: "file", Host: "localhost", Path: "path/to/file"},
			errorMessage("invalid URL: no leading slash in path: path/to/file")},

		{"host_empty", URL("file://host"),
			errorMessage("path is non-local")},
		{"host_root", URL("file://host/"),
			errorMessage("path is non-local")},
		{"host_path", URL("file://host/foo"),
			errorMessage("path is non-local")},
		{"host_nested_path", URL("file://host/path/to/file"),
			errorMessage("path is non-local")},
		// The URLs in the two tests below don't have a valid string representation
		{"host_relative_path", &url.URL{Scheme: "file", Host: "host", Path: "foo"},
			errorMessage("invalid URL: no leading slash in path: foo")},
		{"host_relative_nested_path", &url.URL{Scheme: "file", Host: "host", Path: "path/to/file"},
			errorMessage("invalid URL: no leading slash in path: path/to/file")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, err := file.URLToPOSIXPath(test.url)
			test.expected.assertPath(t, path, err)
		})
	}
}

func TestWindowsPathToURL(t *testing.T) {
	tests := []struct {
		name, path string
		expected   urlAsserter
	}{
		{"empty", "", notAbsErr{"", "no drive letter"}},
		{"drive_letter_without_colon", "c", notAbsErr{"c", "no drive letter"}},
		{"drive_letter", "c:", notAbsErr{"c:", "no path separator after drive letter"}},
		{"drive_root", `c:\`, localPath("/c:/")},
		{"drive_root_slash", "c:/", localPath("/c:/")},
		{"drive_path", `c:\foo`, localPath("/c:/foo")},
		{"drive_nested_path", `c:\path\to\file`, localPath("/c:/path/to/file")},
		{"double_slash", "//", errorMessage("empty UNC host name")},
		{"double_backslash", `\\`, errorMessage("empty UNC host name")},

		{"unc_host", `\\host`, errorMessage("empty UNC share name")},
		{"unc_host_backslash", `\\host\`, errorMessage("empty UNC share name")},
		{"unc_host_empty", `\\\`, errorMessage("empty UNC host name")},
		{"unc_host_empty_share", `\\\share`, errorMessage("empty UNC host name")},
		{"unc_host_share", `\\host\share`, remotePath("/share")},
		{"unc_host_share_backslash", `\\host\share\`, remotePath("/share/")},

		{"unc_localhost_share", `\\localhost\share`, localPath("//localhost/share")},
		{"unc_localhost_share_back", `\\localhost\share\`, localPath("//localhost/share/")},

		{"win32_device_namespace", `\\.\pipe\`,
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_device_namespace_slashes", "//./pipe/",
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_file_namespace", `\\.\pipe\`,
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_device_namespace_slashes", "//./pipe/",
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_file_namespace", `\\?\KernelObjects`,
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_file_namespace_slashes", "//?/KernelObjects",
			errorMessage("no mechanism for translating namespaced paths")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u, err := file.WindowsPathToURL(test.path)
			test.expected.assertURL(t, u, err)
		})
	}
}

func TestURLToWindowsPath(t *testing.T) {
	URL := func(s string) *url.URL { return parseURL(t, s) }

	// url.URL cannot distinguish between an empty authority and path and a
	// missing authority, i.e. "file:" and "file://" produce an identical
	// URL whose string representation is "file:". In Theory, the error
	// messages should be different for both cases. The "file:" case should
	// produce a "no drive letter" error, while the "file://" case should
	// produce "no leading slash".
	emptyURLNoAuthority, emptyURL := URL("file:"), URL("file://")
	require.Equal(t, emptyURLNoAuthority, emptyURL, "Go changed url.Parse()?")
	require.False(t, emptyURLNoAuthority.OmitHost, "Go changed url.Parse()?")
	emptyURLNoAuthority.OmitHost = true
	require.Equal(t, "file:", emptyURLNoAuthority.String(), "Go changed url.Parse()?")

	tests := []struct {
		name     string
		url      *url.URL
		expected pathAsserter
	}{
		{"non_file_url", URL("http://foo/bar"),
			errorMessage("not a file URL")},

		{"empty", emptyURLNoAuthority,
			notAbsErr{"", "no drive letter"}},
		{"bad_escape", URL("file:%%%"),
			errorMessage(`failed to unescape opaque URL: invalid URL escape "%%%"`)},
		{"just_colon", URL("file::"),
			notAbsErr{":", "no drive letter"}},
		{"just_drive_letter", URL("file:c:"),
			notAbsErr{"c:", "no path separator after drive letter"}},
		{"just_drive_path", URL("file:c:foo"),
			notAbsErr{"c:foo", "no path separator after drive letter"}},
		{"just_drive_root_slash", URL("file:c:/"),
			localPath(`c:\`)},
		{"just_drive_root_backslash", URL(`file:c:\`),
			localPath(`c:\`)},

		{"slash", URL("file:/"),
			notAbsErr{"", "no drive letter"}},
		{"colon", URL("file:/:"),
			notAbsErr{":", "no drive letter"}},
		{"drive_letter", URL("file:/c:"),
			notAbsErr{"c:", "no path separator after drive letter"}},
		{"drive_root_slash", URL("file:/c:/"),
			localPath(`c:\`)},
		{"drive_root_backslash", URL(`file:/c:\`),
			localPath(`c:\`)},

		{"escaped_slash", URL("file:/path/to%2ffile"),
			errorMessage("path must not include encoded path separators")},
		{"escaped_backslash", URL("file:/path/to%5cfile"),
			errorMessage("path must not include encoded path separators")},

		{"relative_path", URL("file:foo"),
			notAbsErr{"foo", "no drive letter"}},
		{"relative_nested_path", URL("file:path/to/file"),
			notAbsErr{`path\to\file`, "no drive letter"}},
		{"relative_nested_path_backslash", URL(`file:path\to\file`),
			notAbsErr{`path\to\file`, "no drive letter"}},
		{"relative_path_url", &url.URL{Scheme: "file", OmitHost: true, Path: "foo"},
			notAbsErr{"foo", "no drive letter"}},
		{"relative_nested_path_url", &url.URL{Scheme: "file", OmitHost: true, Path: "path/to/file"},
			notAbsErr{`path\to\file`, "no drive letter"}},
		{"relative_nested_path_backslash_url", &url.URL{Scheme: "file", OmitHost: true, Path: `path\to\file`, RawPath: `path\to\file`},
			notAbsErr{`path\to\file`, "no drive letter"}},

		{"authority_empty", emptyURL,
			notAbsErr{"", "no leading slash"}},
		{"authority_slash", URL("file:///"),
			notAbsErr{"", "no drive letter"}},
		{"authority_colon", URL("file:///:"),
			notAbsErr{":", "no drive letter"}},
		{"authority_drive_letter", URL("file:///c:"),
			notAbsErr{"c:", "no path separator after drive letter"}},
		{"authority_drive_root_slash", URL("file:///c:/"),
			localPath(`c:\`)},
		{"authority_drive_root_backslash", URL(`file:///c:\`),
			localPath(`c:\`)},
		{"authority_relative", &url.URL{Scheme: "file", Path: "foo"},
			errorMessage("invalid URL: no leading slash in path: foo")},

		{"localhost_empty", URL("file://localhost"),
			notAbsErr{"", "no leading slash"}},
		{"localhost_slash", URL("file://localhost/"),
			notAbsErr{"", "no drive letter"}},
		{"localhost_path", URL("file://localhost/foo"),
			notAbsErr{"foo", "no drive letter"}},
		{"localhost_colon", URL("file://localhost/:"),
			notAbsErr{":", "no drive letter"}},
		{"localhost_drive_letter", URL("file://localhost/c:"),
			notAbsErr{"c:", "no path separator after drive letter"}},
		{"localhost_drive_root_slash", URL("file://localhost/c:/"),
			localPath(`c:\`)},
		{"localhost_drive_root_backslash", URL(`file://localhost/c:\`),
			localPath(`c:\`)},

		{"unc_just_host", URL("file://host"),
			errorMessage("empty UNC share name")},
		{"unc_host_slash", URL("file://host/"),
			errorMessage("empty UNC share name")},
		{"unc_host_share", URL("file://host/share"),
			localPath(`\\host\share`)},

		{"unc_path_empty", URL("file:////"),
			errorMessage("empty UNC host name")},
		{"unc_path_just_host", URL("file:////host"),
			errorMessage("empty UNC share name")},
		{"unc_path_host_slash", URL("file:////host/"),
			errorMessage("empty UNC share name")},
		{"unc_path_just_share", URL("file:////\\share"),
			errorMessage("empty UNC host name")},
		{"unc_path_host_share", URL("file:////host/share"),
			localPath(`\\host\share`)},
		{"unc_path_host_share_slash", URL("file:////host/share/"),
			localPath(`\\host\share\`)},
		{"unc_path_empty_share", URL("file:////host//"),
			errorMessage("empty UNC share name")},
		{"unc_relative", &url.URL{Scheme: "file", Host: "host", Path: "foo"},
			errorMessage("invalid URL: no leading slash in path: foo")},

		{"win32_device_namespace_raw", URL(`file:\\.\pipe\`),
			errorMessage(`raw UNC paths are unsupported: \\.\pipe\`)},
		{"win32_file_namespace_raw", URL(`file:\\%3f\KernelObjects`),
			errorMessage(`raw UNC paths are unsupported: \\?\KernelObjects`)},
		{"win32_device_namespace_host", URL(`file://./pipe/`),
			errorMessage("no mechanism for translating namespaced paths")},
		// There's no way to construct a URL with a host of "?". The question
		// mark starts the query part of an URL and percent encodings are only
		// allowed for non-ASCII characters.
		{"win32_file_namespace_host", &url.URL{Scheme: "file", Host: "?", Path: "/pipe/"},
			errorMessage("invalid URL: invalid host: ?")},
		{"win32_device_namespace_path", URL(`file:////./pipe/`),
			errorMessage("no mechanism for translating namespaced paths")},
		{"win32_file_namespace_path", URL(`file:////%3f/KernelObjects`),
			errorMessage("no mechanism for translating namespaced paths")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, err := file.URLToWindowsPath(test.url)
			test.expected.assertPath(t, path, err)
		})
	}
}

func parseURL(t *testing.T, u string) *url.URL {
	parsed, err := url.Parse(u)
	require.NoError(t, err)
	return parsed
}

type urlAsserter interface {
	assertURL(t *testing.T, u *url.URL, err error)
}

type pathAsserter interface {
	assertPath(t *testing.T, path string, err error)
}

type localPath string
type remotePath string
type notAbsErr struct{ path, reason string }
type errorMessage string

func (p localPath) assertURL(t *testing.T, u *url.URL, err error) {
	t.Helper()
	if assert.NoError(t, err) {
		assert.Equal(t, &url.URL{Scheme: "file", Path: string(p)}, u)
	}
}

func (p localPath) assertPath(t *testing.T, path string, err error) {
	t.Helper()
	if assert.NoError(t, err) {
		assert.Equal(t, string(p), path)
	}
}

func (p remotePath) assertURL(t *testing.T, u *url.URL, err error) {
	t.Helper()
	if assert.NoError(t, err) {
		assert.Equal(t, &url.URL{Scheme: "file", Host: "host", Path: string(p)}, u)
	}
}

func (e notAbsErr) assertURL(t *testing.T, u *url.URL, err error) {
	t.Helper()
	if assert.Error(t, err, "URL was %s", u) {
		assert.Nil(t, u)
		e.assertErr(t, err)
	}
}

func (e notAbsErr) assertPath(t *testing.T, path string, err error) {
	t.Helper()
	if assert.Error(t, err, "Path was %q") {
		assert.Empty(t, path)
		e.assertErr(t, err)
	}
}

func (e notAbsErr) assertErr(t *testing.T, err error) {
	t.Helper()
	var pathNotAbsErr *file.PathNotAbsoluteError
	if assert.ErrorAs(t, err, &pathNotAbsErr, "Not a non-absolute path error") {
		assert.Equal(t, file.PathNotAbsoluteError{e.path, e.reason}, *pathNotAbsErr)
	}
}

func (m errorMessage) assertURL(t *testing.T, u *url.URL, err error) {
	t.Helper()
	if assert.Error(t, err, "URL was %s", u) {
		assert.Nil(t, u)
		m.assertErr(t, err)
	}
}

func (m errorMessage) assertPath(t *testing.T, path string, err error) {
	t.Helper()
	if assert.Error(t, err, "Path was %q", path) {
		assert.Empty(t, path)
		m.assertErr(t, err)
	}
}

func (m errorMessage) assertErr(t *testing.T, err error) {
	t.Helper()
	if assert.Error(t, err) {
		assert.Equal(t, string(m), err.Error())
	}
}
