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

package file

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"runtime"
	"strings"
)

// The functions in this file handle conversions of filesystem paths to and from
// file URLs.
//
// Wikipedia has some references: https://en.wikipedia.org/wiki/File_URI_scheme
//
// Notable quotes from RFCs:
//
// * RFC 3986: Uniform Resource Identifier (URI): Generic Syntax, Section 3.2.2, Syntax Components / Authority / Host:
//   https://datatracker.ietf.org/doc/html/rfc3986#section-3.2.2
//   > For example, the "file" URI scheme is defined so that no authority, an
//   > empty host, and "localhost" all mean the end-user's machine, [...]
//
// * RFC 8089: The "file" URI Scheme, Section 2, Syntax:
//   https://datatracker.ietf.org/doc/html/rfc8089#section-2
//   > As a special case, the "file-auth" rule can match the string "localhost"
//   > that is interpreted as "the machine from which the URI is being
//   > interpreted," exactly as if no authority were present. Some current
//   > usages of the scheme incorrectly interpret all values in the authority
//   > of a file URI, including "localhost", as non-local. Yet others interpret
//   > any value as local, even if the "host" does not resolve to the local
//   > machine. To maximize compatibility with previous specifications, users
//   > MAY choose to include an "auth-path" with no "file-auth" when creating a
//   > URI.
//   >
//   > The path component represents the absolute path to the file in the file
//   > system. See Appendix D for some discussion of system-specific concerns
//   > including absolute file paths and file system roots.
//
// * RFC 8089: The "file" URI Scheme, Appendix C, Similar Technologies:
//   https://datatracker.ietf.org/doc/html/rfc8089#appendix-C
//   > The Microsoft Windows API defines Win32 Namespaces for interacting with
//   > files and devices using Windows API functions. These namespaced paths
//   > are prefixed by "\\?\" for Win32 File Namespaces and "\\.\" for Win32
//   > Device Namespaces. There is also a special case for UNC file paths in
//   > Win32 File Namespaces, referred to as "Long UNC", using the prefix
//   > "\\?\UNC\". This specification does not define a mechanism for
//   > translating namespaced paths to or from file URIs.
//
// Also note: There's no such thing as "relative file URLs". Paths can only be
// converted into file URLs and vice versa if they are absolute. The url.Parse()
// function will never produce non-empty paths that don't start with a slash
// (https://github.com/golang/go/issues/6027#issuecomment-66083310). The RFC is
// clear that file URIs contain "absolute paths to files in the file system".
// Resolving relative paths in terms of a base URL would be okay, though.
//
// Node.js has similar implementations for this:
//
// * url.pathToFileURL(...) :
// https://nodejs.org/docs/latest-v19.x/api/url.html#urlpathtofileurlpath
//   * https://github.com/nodejs/node/blob/v19.4.0/lib/internal/url.js#L1528-L1563
//
// * url.fileURLToPath(...) :
// https://nodejs.org/docs/latest-v19.x/api/url.html#urlfileurltopathurl
//   * POSIX: https://github.com/nodejs/node/blob/v19.4.0/lib/internal/url.js#L1468-L1484
//   * Windows: https://github.com/nodejs/node/blob/v19.4.0/lib/internal/url.js#L1433-L1466

func PathToURL(p string) (*url.URL, error) {
	if runtime.GOOS == "windows" {
		return WindowsPathToURL(p)
	}

	return POSIXPathToURL(p)
}

func URLToPath(u *url.URL) (string, error) {
	if runtime.GOOS == "windows" {
		return URLToWindowsPath(u)
	}

	return URLToPOSIXPath(u)
}

type PathNotAbsoluteError struct {
	Path, Reason string
}

func (e *PathNotAbsoluteError) Error() string {
	return fmt.Sprintf("path is not absolute: %q: %s", e.Path, e.Reason)
}

func POSIXPathToURL(p string) (*url.URL, error) {
	if !path.IsAbs(p) {
		return nil, &PathNotAbsoluteError{p, "no leading slash"}
	}

	return &url.URL{Scheme: "file", Path: p}, nil
}

func URLToPOSIXPath(u *url.URL) (string, error) {
	if u.Scheme != "file" {
		return "", errors.New("not a file URL")
	}
	if u.Opaque != "" {
		return "", errors.New("file URL is opaque")
	}

	if err := ensureNoEscapedPathSeparators(u.RawPath, false); err != nil {
		return "", err
	}

	// Non-relative URLs with non-empty paths that don't start with a slash are
	// invalid (https://github.com/golang/go/issues/6027#issuecomment-66083310).
	if u.Path != "" && u.Path[0] != '/' && !u.OmitHost {
		return "", fmt.Errorf("invalid URL: no leading slash in path: %s", u.Path)
	}

	if u.Host != "" && u.Host != "localhost" {
		return "", errors.New("path is non-local")
	}

	if !path.IsAbs(u.Path) {
		return "", &PathNotAbsoluteError{u.Path, "no leading slash"}
	}

	return u.Path, nil
}

func WindowsPathToURL(p string) (*url.URL, error) {

	// Handle UNC paths
	if hasUNCPathPrefix(p) {
		// Treat both forward slashes and backslashes as path separators.
		segments := strings.Split(strings.ReplaceAll(p[2:], "/", `\`), `\`)
		if err := validateUNCPath(segments); err != nil {
			return nil, err
		}

		// Localhost is special in file URLs, hence don't use the usual
		// authority based representation, but the UNC path representation. This
		// ensures that the generated URL will be translated back into an UNC
		// path again.
		// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.3.2
		if segments[0] == "localhost" {
			return &url.URL{Scheme: "file", Path: "//" + strings.Join(segments, "/")}, nil
		}

		// Use the file URI with Authority representation.
		// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.3.1
		return &url.URL{Scheme: "file", Host: segments[0], Path: "/" + strings.Join(segments[1:], "/")}, nil
	}

	if err := ensureAbsWindowsPath(p); err != nil {
		return nil, err
	}

	// Add leading slash and normalize the rest to slashes, too.
	// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.2
	p = "/" + strings.ReplaceAll(p, `\`, "/")
	return &url.URL{Scheme: "file", Path: p}, nil
}

func URLToWindowsPath(u *url.URL) (string, error) {
	if u.Scheme != "file" {
		return "", errors.New("not a file URL")
	}

	host, urlPath, err := unpackWinURL(u)
	if err != nil {
		return "", err
	}

	if uncPath, err := detectUNCPath(host, urlPath); err != nil {
		return "", err
	} else if uncPath != "" {
		return uncPath, nil
	}

	// For URLs without an authority, the leading slash is optional.
	// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.2
	if path.IsAbs(urlPath) {
		urlPath = urlPath[1:]
	} else if host != nil {
		if urlPath != "" {
			// Non-relative URLs with non-empty paths that don't start with a slash are
			// invalid (https://github.com/golang/go/issues/6027#issuecomment-66083310).
			return "", fmt.Errorf("invalid URL: no leading slash in path: %s", urlPath)
		}

		urlPath = strings.ReplaceAll(urlPath, "/", `\`)
		return "", &PathNotAbsoluteError{urlPath, "no leading slash"}
	}

	urlPath = strings.ReplaceAll(urlPath, "/", `\`)
	if err := ensureAbsWindowsPath(urlPath); err != nil {
		return "", err
	}

	return urlPath, nil
}

func unpackWinURL(u *url.URL) (host *string, urlPath string, err error) {
	var rawURLPath string

	// URLs containing no authority but a colon won't be parsed any further
	// by url.Parse(), everything is placed into Opaque.
	if u.Opaque != "" {
		rawURLPath = u.Opaque
		urlPath, err = url.PathUnescape(u.Opaque)
		if err != nil {
			return nil, "", fmt.Errorf("failed to unescape opaque URL: %w", err)
		}
	} else {
		if u.Host != "" || !u.OmitHost {
			host = &u.Host
		}
		rawURLPath = u.RawPath
		urlPath = u.Path
	}

	if err := ensureNoEscapedPathSeparators(rawURLPath, true); err != nil {
		return nil, "", err
	}

	return host, urlPath, nil
}

func detectUNCPath(host *string, urlPath string) (string, error) {
	var segments []string

	switch {
	// UNC paths need to have an authority.
	case host == nil:
		break

	// Treat URLs with hosts as UNC paths.
	// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.3.1
	case *host != "" && *host != "localhost":
		if *host == "?" {
			return "", errors.New("invalid URL: invalid host: ?")
		}

		// Construct UNC path segments by catenating host and path.
		segments = []string{*host}

		if urlPath != "" {
			// Non-relative URLs with non-empty paths that don't start with a slash are
			// invalid (https://github.com/golang/go/issues/6027#issuecomment-66083310).
			if urlPath[0] != '/' {
				return "", fmt.Errorf("invalid URL: no leading slash in path: %s", urlPath)
			}

			// Treat both forward slashes and backslashes as path separators.
			p := strings.ReplaceAll(urlPath[1:], `\`, "/")
			segments = append(segments, strings.Split(p, "/")...)
		}

	// Handle file URIs with UNC paths.
	// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.3.2
	case strings.HasPrefix(urlPath, "//"):
		// Treat both forward slashes and backslashes as path separators.
		p := strings.ReplaceAll(urlPath[2:], `\`, "/")
		segments = strings.Split(p, "/")

	// All other URLs don't represent UNC paths.
	default:
		break
	}

	if segments == nil {
		return "", nil
	}

	if err := validateUNCPath(segments); err != nil {
		return "", err
	}

	// Construct the path from the segments.
	return `\\` + strings.Join(segments, `\`), nil
}

func hasUNCPathPrefix(p string) bool {
	// Windows treats slashes as path separators as well!
	return len(p) > 1 &&
		(p[0] == '\\' || p[0] == '/') &&
		(p[1] == '\\' || p[1] == '/')
}

// Errors out if p contains any percent-encoded forward slashes or backslashes.
// Used to validate escaped URL paths, which shouldn't contain any encoded path
// separators, i.e. they should always use plain slashes. Unescaped backslashes
// are okay for compatibility reasons:
//
// https://datatracker.ietf.org/doc/html/rfc8089#appendix-E.4
//
//	> Historically, some usages have copied entire file paths into the path
//	> components of file URIs. Where DOS or Windows file paths were thus
//	> copied, the resulting URI strings contained unencoded backslash "\"
//	> characters, which are forbidden [...]
//	>
//	> It may be possible to translate or update such an invalid file URI by
//	> replacing all backslashes "\" with slashes "/" if it can be determined
//	> with reasonable certainty that the backslashes are intended as path
//	> separators.
//
// The Node.js implementation does a similar thing ...
func ensureNoEscapedPathSeparators(rawPath string, includeBackslash bool) error {
	for {
		idx := strings.IndexByte(rawPath, '%')
		if idx < 0 || len(rawPath) < idx+3 {
			return nil
		}

		// Slash is %2f and backslash is %5c
		if (rawPath[idx+1] == '2' && (rawPath[idx+2] == 'f' || rawPath[idx+2] == 'F')) ||
			(includeBackslash && (rawPath[idx+1] == '5' && (rawPath[idx+2] == 'c' || rawPath[idx+2] == 'C'))) {
			break
		}

		rawPath = rawPath[idx+3:]
	}

	return errors.New("path must not include encoded path separators")
}

func ensureAbsWindowsPath(p string) error {
	// https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file#fully-qualified-vs-relative-paths

	// Raw UNC paths are not covered by the RFCs, but should at least raise a clear error.
	if hasUNCPathPrefix(p) {
		return fmt.Errorf("raw UNC paths are unsupported: %s", p)
	}

	if len(p) < 2 || p[1] != ':' || !(p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z') {
		return &PathNotAbsoluteError{p, "no drive letter"}
	}

	// Windows treats slashes as path separators as well!
	if len(p) < 3 || (p[2] != '\\' && p[2] != '/') {
		return &PathNotAbsoluteError{p, "no path separator after drive letter"}
	}

	return nil
}

// Validates UNC path segments without the leading backslashes.
// Exemplary UNC Path: \\hostname\sharename[\resource]
//
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-dtyp/62e862f4-2a51-452e-8eeb-dc4ff5ee33cc
func validateUNCPath(segments []string) error {
	// Just validate stuff for existence, don't bother to validate the format...
	if len(segments) < 1 || segments[0] == "" {
		return errors.New("empty UNC host name")
	}
	if len(segments) < 2 || segments[1] == "" {
		return errors.New("empty UNC share name")
	}

	// Error out when encountering namespaces.
	// https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file#win32-file-namespaces
	// https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file#win32-device-namespaces
	if segments[0] == "?" || segments[0] == "." {
		// https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation
		// > The "\\?\" prefix can also be used with paths constructed
		// > according to the universal naming convention (UNC). To specify
		// > such a path using UNC, use the "\\?\UNC\" prefix

		// Theoretically, one could treat "\\?\" prefixed paths as regular local
		// paths and "\\?\UNC\" as UNC paths. But this would result in
		// non-stable roundtrip conversions between paths and URLs (the URL
		// would loose the namespace prefix).
		return errors.New("no mechanism for translating namespaced paths")
	}

	return nil
}
