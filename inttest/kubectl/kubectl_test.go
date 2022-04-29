package kubectl

import (
	"strings"
	"testing"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type KubectlSuite struct {
	common.FootlooseSuite
}

const pluginContent = `#!/usr/bin/env sh

echo foo-plugin
`

func (s *KubectlSuite) TestEmbeddedKubectl() {
	s.Require().NoError(s.InitController(0))
	s.PutFile(s.ControllerNode(0), "/bin/kubectl-foo", pluginContent)

	ssh, err := s.SSH(s.ControllerNode(0))
	s.Require().NoError(err, "failed to SSH into controller")
	defer ssh.Disconnect()

	_, err = ssh.ExecWithOutput("chmod +x /bin/kubectl-foo")
	s.Require().NoError(err)
	_, err = ssh.ExecWithOutput("ln -s /usr/bin/k0s /usr/bin/kubectl")
	s.Require().NoError(err)

	s.T().Log("Check that different ways to call kubectl subcommand work")

	tests := []struct {
		Name    string
		Command string
		Check   func(t *testing.T, output string, err error)
	}{
		{
			Name:    "full subcommand name",
			Command: "k0s kubectl version",
			Check: func(t *testing.T, output string, e error) {
				assert.Contains(t, output, "Client Version: version.Info")
			},
		},
		{
			Name:    "short subcommand name",
			Command: "k0s kc version",
			Check: func(t *testing.T, output string, e error) {
				assert.Contains(t, output, "Client Version: version.Info")
			},
		},
		{
			Name:    "full command arguments",
			Command: "k0s kubectl version -v 8",
			Check: func(t *testing.T, output string, e error) {
				// Check for debug log messages
				assert.True(t, strings.Contains(output, "round_trippers.go"))
			},
		},
		{
			Name:    "short command arguments",
			Command: "k0s kc version -v 8",
			Check: func(t *testing.T, output string, e error) {
				// Check for debug log messages
				assert.True(t, strings.Contains(output, "round_trippers.go"))
			},
		},
		{
			Name:    "full command plugin loader",
			Command: "k0s kubectl foo",
			Check: func(t *testing.T, output string, e error) {
				assert.Equal(t, output, "foo-plugin")
			},
		},
		{
			Name:    "short command plugin loader",
			Command: "k0s kc foo",
			Check: func(t *testing.T, output string, e error) {
				assert.Equal(t, output, "foo-plugin")
			},
		},
		{
			Name:    "symlink command",
			Command: "kubectl version",
			Check: func(t *testing.T, output string, e error) {
				assert.Contains(t, output, "Client Version: version.Info")
			},
		},
		{
			Name:    "symlink arguments",
			Command: "kubectl version -v 8",
			Check: func(t *testing.T, output string, e error) {
				// Check for debug log messages
				assert.Contains(t, output, "round_trippers.go")
			},
		},
		{
			Name:    "symlink plugin loader",
			Command: "k0s kubectl foo",
			Check: func(t *testing.T, output string, e error) {
				assert.Equal(t, "foo-plugin", output)
			},
		},
	}
	for _, test := range tests {
		s.T().Run(test.Name, func(t *testing.T) {
			t.Log("Executing", test.Command)
			output, err := ssh.ExecWithOutput(test.Command)
			t.Cleanup(func() {
				if t.Failed() {
					t.Log("Error: ", err)
					t.Log("Output: ", output)
				}
			})
			assert.NoError(t, err)
			test.Check(t, output, err)
		})
	}
}

func TestKubectlCommand(t *testing.T) {
	suite.Run(t, &KubectlSuite{
		common.FootlooseSuite{
			ControllerCount: 1,
		},
	})
}
