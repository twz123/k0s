package backends

import (
	"github.com/pulumi/pulumi-tls/sdk/v4/go/tls"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Host struct {
	Name pulumi.StringOutput
	Role pulumi.StringOutput
	IPv4 pulumi.StringOutput
}

type Backend struct {
	Hosts       []Host
	SSHUsername pulumi.StringOutput
	SSHKey      *tls.PrivateKey
}
