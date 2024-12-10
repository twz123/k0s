/*
Copyright 2024 k0s authors

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

package airgap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/distribution/reference"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/airgap"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/kubectl/pkg/util/term"
)

type imageBundleOpts struct {
	bundler airgap.ImageBundler
	outPath string
	stdout  func() io.Writer
}

func NewAirgapBundleImagesCmd(log logrus.FieldLogger) *cobra.Command {
	opts := imageBundleOpts{
		bundler: airgap.ImageBundler{
			Log: log,
		},
	}

	cmd := &cobra.Command{
		Use:   "bundle-images [flags] [file]",
		Short: "Bundles images in a tarball needed for airgapped installations",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := config.CallParentPersistentPreRun(cmd, args); err != nil {
				return err
			}
			if opts.outPath != "" {
				return nil
			}

			return enforceNoTerminal(cmd.OutOrStdout())
		},
	}

	cmd.PersistentFlags().StringVarP(&opts.outPath, "output", "o", "", "output file path (writes to standard output if omitted)")
	cmd.Flags().Var((*insecureRegistryFlag)(&opts.bundler.InsecureRegistries), "insecure-registries", "one of "+strings.Join(insecureRegistryFlagValues[:], ", "))
	cmd.Flags().StringArrayVar(&opts.bundler.RegistriesConfigPaths, "registries-config", nil, "paths to the authentication files for image registries")

	opts.stdout = cmd.OutOrStdout
	cmd.AddCommand(newFromConfigCommand(&opts))
	cmd.AddCommand(newFromStdinCommand(&opts))
	cmd.AddCommand(newFromFileCommand(&opts))
	return cmd
}

func newFromConfigCommand(opts *imageBundleOpts) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "from-config",
		Short: "Bundles images for the current cluster configuration",
		Long: `Bundles images for the current cluster configuration.
Builds the list of images in the same way as the list-images sub-command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			cmdOpts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}

			clusterConfig, err := cmdOpts.K0sVars.NodeConfig()
			if err != nil {
				return fmt.Errorf("failed to get config: %w", err)
			}

			var imageRefs []reference.Named
			for image := range airgap.ImagesInSpec(clusterConfig.Spec, all) {
				uri := image.URI()
				ref, err := reference.ParseNormalizedNamed(uri)
				if err != nil {
					return fmt.Errorf("while parsing %q: %w", uri, err)
				}
				imageRefs = append(imageRefs, ref)
			}

			return opts.runBundler(cmd.Context(), imageRefs)
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "include all images, even if they are not used in the current configuration")
	return cmd
}

func newFromFileCommand(opts *imageBundleOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "from-file [flags] file",
		Short: "Bundles images read from the given file",
		Long: `Bundles images read from the given file, line by line. Surrounding whitespace is
ignored, lines whose first non-whitespace character is a # are ignored.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			imageRefs, err := parseReferencesFromFile(args[0])
			if err != nil {
				return err
			}
			return opts.runBundler(cmd.Context(), imageRefs)
		},
	}
}

func newFromStdinCommand(opts *imageBundleOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "from-stdin",
		Short: "Bundles images read from standard input",
		Long: `Bundles images read from standard input, line by line. Surrounding whitespace is
ignored, lines whose first non-whitespace character is a # are ignored.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			imageRefs, err := parseReferencesFromReader(cmd.InOrStdin())
			if err != nil {
				return err
			}
			return opts.runBundler(cmd.Context(), imageRefs)
		},
	}
}

func parseReferencesFromFile(path string) (_ []reference.Named, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return parseReferencesFromReader(f)
}

func parseReferencesFromReader(in io.Reader) ([]reference.Named, error) {
	lines := bufio.NewScanner(in)

	var (
		imageRefs []reference.Named
		lineNum   uint
	)
	for lines.Scan() {
		lineNum++
		line := lines.Bytes()
		if len(line) > 0 && line[0] != '#' {
			image := string(line)
			ref, err := reference.ParseNormalizedNamed(image)
			if err != nil {
				return nil, fmt.Errorf("while parsing line %d: %q: %w", lineNum, image, err)
			}
			imageRefs = append(imageRefs, ref)
		}
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}

	return imageRefs, nil
}

func (o *imageBundleOpts) runBundler(ctx context.Context, refs []reference.Named) (err error) {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var out io.Writer
	if o.outPath == "" {
		out = o.stdout()
		if err := enforceNoTerminal(out); err != nil {
			return err
		}
	} else {
		f, err := file.AtomicWithTarget(o.outPath).Open()
		if err != nil {
			return err
		}
		defer func() {
			if err == nil {
				err = f.Finish()
			} else if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}()
		out = f
	}

	buffered := bufio.NewWriter(out)
	if err := o.bundler.Run(ctx, refs, out); err != nil {
		return err
	}
	return buffered.Flush()
}

func enforceNoTerminal(out io.Writer) error {
	if !term.IsTerminal(out) {
		return nil
	}

	return errors.New("cowardly refusing to write binary data to a terminal")
}

type insecureRegistryFlag airgap.InsecureRegistryKind

var insecureRegistryFlagValues = [...]string{
	airgap.NoInsecureRegistry:    "no",
	airgap.SkipTLSVerifyRegistry: "skip-tls-verify",
	airgap.PlainHTTPRegistry:     "plain-http",
}

func (i insecureRegistryFlag) String() string {
	if i := int(i); i < len(insecureRegistryFlagValues) {
		return insecureRegistryFlagValues[i]
	} else {
		return strconv.Itoa(i)
	}
}

func (i *insecureRegistryFlag) Set(value string) error {
	idx := slices.Index(insecureRegistryFlagValues[:], value)
	if idx >= 0 {
		*i = insecureRegistryFlag(idx)
	}

	return errors.New("must be one of " + strings.Join(insecureRegistryFlagValues[:], ", "))
}

func (insecureRegistryFlag) Type() string {
	return "string"
}
