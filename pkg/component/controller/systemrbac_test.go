package controller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/component/controller"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemRBAC(t *testing.T) {
	manifestsDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(manifestsDir, "bootstraprbac"), 0755))

	underTest := controller.SystemRBAC{
		ManifestsDir: manifestsDir,
		Clients:      testutil.NewFakeClientFactory(),
	}

	err := underTest.Init(context.TODO())
	assert.NoError(t, err)
	assert.FileExists(t, filepath.Join(manifestsDir, "bootstraprbac", "ignored.txt"))

	err = underTest.Start(context.TODO())
	assert.NoError(t, err)
}
