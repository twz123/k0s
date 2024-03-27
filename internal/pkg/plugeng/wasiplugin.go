package plugeng

import (
	"fmt"
	"io/fs"
)

type WASIPlugin struct {
	fs fs.FS
}

func (p *WASIPlugin) Code() (_ []byte, err error) {
	code, err := fs.ReadFile(p.fs, "plugin.wasm")
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin module: %w", err)
	}

	return code, nil
}

func (p *WASIPlugin) Data() (fs.FS, error) {
	return fs.Sub(p.fs, "data")
}
