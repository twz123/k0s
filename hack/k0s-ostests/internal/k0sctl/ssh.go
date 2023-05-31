package k0sctl

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/adrg/xdg"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"golang.org/x/crypto/ssh/agent"
)

func doSSH(ctx *pulumi.Context, resource pulumi.Resource) (err error) {
	l := &pulumi.LogArgs{Resource: resource, Ephemeral: true}

	// Create an SSH agent implementation
	sshAgent := agent.NewKeyring()

	// Get the path to the SSH agent socket
	socketPath, err := getSSHAgentSocketPath(ctx)
	if err != nil {
		return err
	}
	// defer func() { err = errors.Join(err, os.Remove(socketPath)) }()

	// Create the SSH agent listener on the socket path
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create SSH agent listener: %w", err)
	}

	ctx.Log.Info("SSH agent listening", l)

	go func() {
		<-ctx.Context().Done()

		// Stop listening
		ctx.Log.Info("Stopping SSH agent", l)
		if err := listener.Close(); err != nil {
			ctx.Log.Error(fmt.Sprintf("Failed to stop SSH agent: %v", err), l)
		}

		// Remove the socket file
		if err = os.Remove(socketPath); err != nil {
			ctx.Log.Error(fmt.Sprintf("Failed to remove SSH agent socket: %v", err), l)
		}
	}()

	go func() {
		// Accept connections and serve the SSH agent requests
		for {
			conn, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}

			if err != nil {
				ctx.Log.Error(fmt.Sprintf("Failed to accept connection: %v", err), l)
				continue
			}

			var closed atomic.Bool
			closeConn := func() {
				if !closed.Swap(true) {
					if err := conn.Close(); err != nil {
						ctx.Log.Error(fmt.Sprintf("Failed to close SSH agent connection: %v", err), l)
					}
				}
			}

			go func() {
				<-ctx.Context().Done()
				closeConn()
			}()

			go func() {
				defer closeConn()
				// Serve the SSH agent requests on the connection
				err := agent.ServeAgent(sshAgent, conn)
				if err != nil && !closed.Load() {
					ctx.Log.Error(fmt.Sprintf("Failed to serve SSH agent request: %v", err), l)
				}
			}()
		}
	}()

	return nil
}

// Gets the path to the SSH agent socket
func getSSHAgentSocketPath(ctx *pulumi.Context) (_ string, err error) {
	f, err := os.CreateTemp(xdg.RuntimeDir, fmt.Sprintf("%s-%s-k0sctl-ssh-agent-*.sock", ctx.Project(), ctx.Stack()))
	if err != nil {
		return "", fmt.Errorf("failed to create temporary socket: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close(), os.Remove(f.Name())) }()
	return filepath.Abs(f.Name())
}
