package aws

import (
	"fmt"
	"k0s-ostests/internal"
	"k0s-ostests/internal/backends"

	ec2n "github.com/pulumi/pulumi-aws-native/sdk/go/aws/ec2"
	ec2c "github.com/pulumi/pulumi-aws/sdk/v5/go/aws/ec2"
	"github.com/pulumi/pulumi-tls/sdk/v4/go/tls"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func New(ctx *pulumi.Context, prefix internal.NamePrefix) (*backends.Backend, error) {
	key, err := tls.NewPrivateKey(ctx, "ssh", &tls.PrivateKeyArgs{
		Algorithm: pulumi.String("ED25519"),
	})
	if err != nil {
		return nil, err
	}

	keyPair, err := ec2n.NewKeyPair(ctx, "ssh", &ec2n.KeyPairArgs{
		KeyName:           prefix.PrefixedName("ssh"),
		PublicKeyMaterial: key.PublicKeyOpenssh,
	})
	if err != nil {
		return nil, err
	}

	network, err := SetupNetwork(ctx, prefix)
	if err != nil {
		return nil, err
	}

	ami, err := ec2c.LookupAmi(ctx, &ec2c.LookupAmiArgs{
		NameRegex:  pulumi.StringRef(`^alpine-3\.17\.\d+-x86_64-bios-tiny($|-.*)`),
		MostRecent: pulumi.BoolRef(true),
		Owners:     []string{"538276064493"},
		Filters: []ec2c.GetAmiFilter{
			{Name: "name", Values: []string{"alpine-3.17.*-x86_64-bios-tiny*"}},
			{Name: "root-device-type", Values: []string{"ebs"}},
			{Name: "virtualization-type", Values: []string{"hvm"}},
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	var controllers [1]backends.Host
	controllerRole := pulumi.String("controller")
	for i := range controllers {
		name := fmt.Sprintf("controller-%d", i)
		instanceName := prefix.PrefixedName(name)
		instance, err := ec2c.NewInstance(ctx, name, &ec2c.InstanceArgs{
			Ami:          pulumi.String(ami.Id),
			InstanceType: pulumi.String("t2.medium"),
			SubnetId:     network.subnetID,

			Tags: pulumi.StringMap{
				"Name":                           instanceName,
				"k0sctl.k0sproject.io/host-role": controllerRole,
			},

			KeyName:                  keyPair.KeyName,
			AssociatePublicIpAddress: pulumi.Bool(true),
			VpcSecurityGroupIds:      network.securityGroupIDs,

			// TODO: Is this required?
			RootBlockDevice: ec2c.InstanceRootBlockDeviceArgs{
				VolumeType: pulumi.String("gp2"),
				VolumeSize: pulumi.Int(20),
			},
		})
		if err != nil {
			return nil, err
		}

		host := backends.Host{
			Name: instanceName,
			Role: controllerRole.ToStringOutput(),
			IPv4: instance.PublicIp,
		}
		controllers[i] = host
	}

	return &backends.Backend{
		Hosts:       controllers[:],
		SSHUsername: pulumi.String("alpine").ToStringOutput(),
		SSHKey:      key,
	}, nil
}
