package aws

import (
	"k0s-ostests/internal"

	awsc "github.com/pulumi/pulumi-aws/sdk/v5/go/aws"
	ec2c "github.com/pulumi/pulumi-aws/sdk/v5/go/aws/ec2"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type network struct {
	subnetID         pulumi.StringInput
	securityGroupIDs pulumi.StringArrayInput
}

func SetupNetwork(ctx *pulumi.Context, prefix internal.NamePrefix) (*network, error) {
	defaultVPC, err := ec2c.LookupVpc(ctx, &ec2c.LookupVpcArgs{
		Default: pulumi.BoolRef(true),
		State:   pulumi.StringRef("available"),
	})
	if err != nil {
		return nil, err
	}

	randomZone, err := someRandomZone(ctx)
	if err != nil {
		return nil, err
	}

	defaultSubnet, err := ec2c.NewDefaultSubnet(ctx, "default", &ec2c.DefaultSubnetArgs{
		AvailabilityZone: randomZone,
	})
	if err != nil {
		return nil, err
	}

	_, err = ec2c.NewRouteTableAssociation(ctx, "az_default", &ec2c.RouteTableAssociationArgs{
		SubnetId:     defaultSubnet.ID(),
		RouteTableId: pulumi.String(defaultVPC.MainRouteTableId),
	})
	if err != nil {
		return nil, err
	}

	secGrp, err := allAccessSecGrp(ctx, prefix, pulumi.String(defaultVPC.Id))
	if err != nil {
		return nil, err
	}

	return &network{
		subnetID:         defaultSubnet.ID(),
		securityGroupIDs: pulumi.StringArray{secGrp.ID()},
	}, nil
}

func someRandomZone(ctx *pulumi.Context) (out pulumi.StringOutput, _ error) {
	availableZones, err := awsc.GetAvailabilityZones(ctx, &awsc.GetAvailabilityZonesArgs{
		State: pulumi.StringRef("available"),
	})
	if err != nil {
		return out, err
	}

	shuffeledZones, err := random.NewRandomShuffle(ctx, "shuffled-availability-zones", &random.RandomShuffleArgs{
		Inputs: pulumi.ToStringArray(availableZones.Names),
	})
	if err != nil {
		return out, err
	}

	return shuffeledZones.Results.Index(pulumi.Int(0)), nil
}

func allAccessSecGrp(ctx *pulumi.Context, prefix internal.NamePrefix, vpcID pulumi.StringInput) (*ec2c.SecurityGroup, error) {
	name := prefix.PrefixedName("all-access")
	return ec2c.NewSecurityGroup(ctx, "all_access", &ec2c.SecurityGroupArgs{
		VpcId: vpcID.ToStringPtrOutput(),

		Name: name,
		Tags: pulumi.StringMap{"Name": name},

		Description: pulumi.String("Allow ALL traffic from/to the outside"),

		Ingress: ec2c.SecurityGroupIngressArray{
			&ec2c.SecurityGroupIngressArgs{
				Description: pulumi.String("Allow ALL traffic from the outside"),
				FromPort:    pulumi.Int(0),
				ToPort:      pulumi.Int(0),
				Protocol:    pulumi.String("-1"),
				CidrBlocks:  pulumi.ToStringArray([]string{"0.0.0.0/0"}),
			},
		},

		Egress: ec2c.SecurityGroupEgressArray{
			&ec2c.SecurityGroupEgressArgs{
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				Protocol:   pulumi.String("-1"),
				CidrBlocks: pulumi.ToStringArray([]string{"0.0.0.0/0"}),
			},
		},
	})
}
