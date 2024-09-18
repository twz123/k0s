package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	internalio "github.com/k0sproject/k0s/internal/io"
	"github.com/k0sproject/k0s/pkg/build"
)

func Download(ctx context.Context, url string, fileName *string, target io.Writer) (err error) {
	// Create the request.
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("invalid download request: %w", err)
	}
	req.Header.Set("User-Agent", "k0s/"+build.Version)

	// Set up a context and a means of detecting download staleness.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	staleDetector := time.NewTimer(20 * time.Second)
	defer staleDetector.Stop()
	go func() {
		select {
		case <-staleDetector.C:
			cancel(errors.New("download is stale"))
		case <-ctx.Done():
			return
		}
	}()

	// Execute the request.
	client := http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = fmt.Errorf("%w (%w)", cause, err)
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Determine the content length, if provided.
	body := io.Reader(resp.Body)
	var contentLength uint64
	if contentLengthHeader := resp.Header.Get("Content-Length"); contentLengthHeader != "" {
		contentLength, err := strconv.ParseUint(contentLengthHeader, 10, 63 /* to fit into int64 */)
		if err != nil {
			return fmt.Errorf("invalid Content-Length: %w", err)
		}
		if contentLength == 0 {
			return nil // Nothing to download, short-circuit.
		}
		body = io.LimitReader(body, int64(contentLength))
	}

	// Determine file name, if requested.
	if fileName != nil {
		name, err := fileNameFor(resp)
		if err != nil {
			return fmt.Errorf("failed to determine file name from response: %w", err)
		}
		if err := validateFileName(name); err != nil {
			return fmt.Errorf("invalid file name %q in response: %w", name, err)
		}
		*fileName = name
	}

	// Measure download throughput.
	const (
		windowCount = 60
		windowSize  = 1 * time.Second
	)
	throughputMeasurement := internalio.NewThroughputWriter(windowCount, internalio.Sliding(windowSize), func(numBytes uint64, numWindows uint) (err error) {
		if staleDetector.Stop() {
			defer func() {
				if err == nil {
					staleDetector.Reset(20 * time.Second)
				}
			}()
		}

		if numWindows < windowCount {
			return nil
		}

		bytesPerSec := float64(numBytes) / float64(numWindows) / windowSize.Seconds()
		if contentLength > 0 {
			estimatedOverallDuration := time.Duration((float64(contentLength) / bytesPerSec) * float64(time.Second))
			if estimatedOverallDuration > (24 * time.Hour) {
				return fmt.Errorf("slow download: estimated overall download time using %.f B/sec for %d bytes is %s", bytesPerSec, contentLength, estimatedOverallDuration)
			}
		} else if bytesPerSec < 3*1024 { // This is below GPRS ...
			return fmt.Errorf("slow download: %.f B/sec on average", bytesPerSec)
		}

		return nil
	})

	// Run the actual data transfer.
	if _, err := io.Copy(io.MultiWriter(throughputMeasurement, target), body); err != nil {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = fmt.Errorf("%w (%w)", cause, err)
		}

		return fmt.Errorf("while downloading: %w", err)
	}

	return nil
}

func fileNameFor(resp *http.Response) (string, error) {
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition#as_a_response_header_for_the_main_body
	if contentDisposition := resp.Header.Get("Content-Disposition"); contentDisposition != "" {
		_, params, err := mime.ParseMediaType(contentDisposition)
		if err != nil {
			return "", fmt.Errorf("invalid Content-Disposition: %w", err)
		}
		// FIXME: some tests for MIME stuff in Golang...
		// Ignore the "filename*" param for now, as it's not easy to parse...
		if fileName, ok := params["filename"]; ok {
			return fileName, nil
		}
	}

	// FIXME: can we do it this simple?
	return path.Base(resp.Request.URL.Path), nil
}

func validateFileName(fileName string) error {
	if len(fileName) > 255 {
		return errors.New("too long")
	}

	switch fileName {
	case "":
		return errors.New("empty")
	case ".":
		return errors.New("dot")
	case "..":
		return errors.New("dotdot")
	}

	// https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	if base, _, _ := strings.Cut(fileName, "."); len(base) < 6 {
		switch strings.ToLower(base) {
		case "con", "prn", "aux", "nul",
			"com0", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
			"lpt0", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
			"com¹", "com²", "com³", "lpt¹", "lpt²", "lpt³":
			return errors.New("reserved name: " + fileName)
		}
	}

	var (
		pos int
		ch  rune
	)
	for pos, ch = range fileName {
		var ok bool
		switch ch {
		case '\\', '/': // path separators
		case '<', '>', '|': // redirections and pipes are not safe for shells and are not allowed in Windows file names
		case ':': // used to specify Windows drive letters and not allowed in Windows file names
		case '"': // used to encapsulate file names in shells and not allowed in Windows file names
		case '?', '*': // shell wildcards and not allowed in Windows file names
		default:
			ok = true
		}
		if !ok {
			return fmt.Errorf("unsafe character %c at byte %d", ch, pos)
		}

		if unicode.Is(unicode.Other, ch) {
			return fmt.Errorf("unsafe special character 0x%x at byte %d", int32(ch), pos)
		}

		if pos == 0 && unicode.IsSpace(ch) {
			return fmt.Errorf("leading whitespace character 0x%x", int32(ch))
		}
	}

	switch {
	case ch == '.':
		return fmt.Errorf("trailing dot")
	case unicode.IsSpace(ch):
		return fmt.Errorf("trailing whitespace character 0x%x", int32(ch))
	}

	return nil
}
