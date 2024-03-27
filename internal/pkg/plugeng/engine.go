package plugeng

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"

	internallog "github.com/k0sproject/k0s/internal/pkg/log"

	"github.com/sirupsen/logrus"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

type Plugin interface {
	Code() ([]byte, error)
	Data() (fs.FS, error)
}

type Executor struct {
	// Plugin Plugin

	Code []byte
	FS   fs.FS
	Log  logrus.FieldLogger
}

func (e *Executor) DoThatStuff(ctx context.Context) (_ uint32, err error) {
	// code, err := p.Plugin.Code()
	// if err != nil {
	// 	return 0, fmt.Errorf("failed to load plugin code: %w", code)
	// }

	// Combine the above into our baseline config, overriding defaults.
	config := wazero.NewModuleConfig().
		WithFSConfig(wazero.NewFSConfig().WithDirMount("/tmp", "/tmp")).
		WithRandSource(rand.Reader).
		WithArgs("k0s-controller-plugin")

	if e.Log != nil {
		config = config.
			WithStdout(internallog.NewWriter(e.Log.WithField("stream", "stdout"))).
			WithStderr(internallog.NewWriter(e.Log.WithField("stream", "stderr")))
	}

	rt := wazero.NewRuntime(ctx)
	defer func() { err = errors.Join(err, rt.Close(ctx)) }()
	// Instantiate WASI, which implements system I/O such as console output.
	wasi, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, wasi.Close(ctx)) }()

	// InstantiateModule runs the "_start" function, WASI's "main".
	// * Set the program name (arg[0]) to "wasi"; arg[1] should be "/test.txt".
	if _, err = rt.InstantiateWithConfig(ctx, e.Code, config); err != nil {
		// Note: Most compilers do not exit the module after running "_start",
		// unless there was an error. This allows you to call exported functions.
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return exitErr.ExitCode(), nil
		}

		return 0, err
	}

	return 0, nil
}

type pluginFS struct {
	executorFS, dataFS fs.FS
}
