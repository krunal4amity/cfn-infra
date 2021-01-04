package main

import (
	"context"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/kafka"
	"github.com/aws/aws-sdk-go/service/kafka/kafkaiface"
	"github.com/tj/assert"
	"os"
	"testing"
)

var (
	key        = os.Getenv("AWS_ACCESS_KEY_ID")
	pass       = os.Getenv("AWS_SECRET_ACCESS_KEY")
	configName = "krunal3"
	vpcId      = "vpc-06729a303030c2a78"
	serverProp = []byte(`
auto.create.topics.enable = true
zookeeper.connection.timeout.ms = 2000
log.roll.ms = 604800000
       `)
)

type mockMsk struct {
	kafkaiface.KafkaAPI
	listResp   kafka.ListConfigurationsOutput
	createResp kafka.CreateConfigurationOutput
}

type mockEc2 struct {
	ec2iface.EC2API
	descVpc ec2.DescribeVpcsOutput
	descRt  ec2.DescribeRouteTablesOutput
}

func (m *mockMsk) ListConfigurationsWithContext(ctx aws.Context, param *kafka.ListConfigurationsInput, opts ...request.Option) (*kafka.ListConfigurationsOutput, error) {
	return &m.listResp, nil
}

func (m *mockMsk) CreateConfigurationWithContext(ctx aws.Context, param *kafka.CreateConfigurationInput, opts ...request.Option) (*kafka.CreateConfigurationOutput, error) {
	return &m.createResp, nil
}

func (m *mockEc2) DescribeVpcsWithContext(ctx aws.Context, param *ec2.DescribeVpcsInput, opts ...request.Option) (*ec2.DescribeVpcsOutput, error) {
	return &m.descVpc, nil
}

func (m *mockEc2) DescribeRouteTablesWithContext(ctx aws.Context, param *ec2.DescribeRouteTablesInput, opts ...request.Option) (*ec2.DescribeRouteTablesOutput, error) {
	return &m.descRt, nil
}

func Test_MockListMSKConfig(t *testing.T) {
	cases := []struct {
		Resp     kafka.ListConfigurationsOutput
		Expected string
	}{
		{
			Resp: kafka.ListConfigurationsOutput{
				Configurations: []*kafka.Configuration{
					{
						Name:           aws.String("SampleConfig1"),
						Arn:            aws.String("arn:aws:msk:us-west-2:1234567891:mymskconfig"),
						LatestRevision: &kafka.ConfigurationRevision{Revision: aws.Int64(1)},
					},
				},
			},
			Expected: "arn:aws:msk:us-west-2:1234567891:mymskconfig",
		},
	}

	for _, c := range cases {
		mskApi := MSKclient{Client: &mockMsk{listResp: c.Resp}}
		arn, err := mskApi.listConfigurations(context.Background(), "SampleConfig1")
		assert.Nil(t, err)
		assert.Equal(t, c.Expected, arn)
	}
}

func Test_MockCreateMSKConfig(t *testing.T) {
	cases := []struct {
		Resp     kafka.CreateConfigurationOutput
		Expected string
	}{
		{
			Resp: kafka.CreateConfigurationOutput{
				Name:           aws.String("SampleConfig1"),
				Arn:            aws.String("arn:aws:msk:us-west-2:1234567891:mymskconfig"),
				LatestRevision: &kafka.ConfigurationRevision{Revision: aws.Int64(1)},
			},
			Expected: "arn:aws:msk:us-west-2:1234567891:mymskconfig",
		},
	}

	for _, c := range cases {
		mskApi := MSKclient{Client: &mockMsk{createResp: c.Resp}}
		arn, err := mskApi.createConfig(context.Background(), ClusterConfig{"SampleConfig1", "mymskconfig", []string{"1.1.1", "2.2.1"}}, serverProp)
		assert.Nil(t, err)
		t.Log("mockCreateMSKConfig = arn is :", arn)
		assert.Equal(t, c.Expected, arn)
	}
}

func Test_MockDescribeVpc(t *testing.T) {
	cases := []struct {
		Resp     ec2.DescribeVpcsOutput
		Expected string
	}{
		{
			Resp: ec2.DescribeVpcsOutput{
				Vpcs: []*ec2.Vpc{
					{
						VpcId:     aws.String("vpc-1234"),
						CidrBlock: aws.String("10.0.0.0/12"),
					},
				},
			},
			Expected: "10.0.0.0/12",
		},
	}

	for _, c := range cases {
		ec2Api := Ec2Client{Client: &mockEc2{descVpc: c.Resp}}
		vpcId, err := ec2Api.vpcCidr(context.Background(), "vpc-1234")
		t.Log("vpc id: ", vpcId)
		assert.Nil(t, err)
		for _, vpc := range c.Resp.Vpcs {
			assert.Equal(t, c.Expected, aws.StringValue(vpc.CidrBlock))
		}
	}
}

func Test_MockDescribeRouteTables(t *testing.T) {

	cases := []struct {
		Resp     ec2.DescribeRouteTablesOutput
		Expected []string
	}{
		{
			Resp: ec2.DescribeRouteTablesOutput{
				RouteTables: []*ec2.RouteTable{
					{
						RouteTableId: aws.String("rt-1234"),
						VpcId:        aws.String("vpc-1234"),
						Routes: []*ec2.Route{
							{
								NatGatewayId: aws.String("nat-1234"),
							},
						},
						Associations: []*ec2.RouteTableAssociation{
							{
								SubnetId:     aws.String("sub-1234"),
								RouteTableId: aws.String("rt-1234"),
							},
						},
					},
				},
			},
			Expected: []string{"sub-1234"},
		},
	}

	for _, c := range cases {
		ec2Api := Ec2Client{Client: &mockEc2{descRt: c.Resp}}
		rts, err := ec2Api.privSubnets(context.Background(), "vpc-1234")
		t.Log("subnets: ", rts)
		assert.Nil(t, err)
		//assert.Equal(t,c.Expected,rts)
	}
}

func TestMSKMethods(t *testing.T) {
	if key == "" || pass == "" {
		t.SkipNow()
	}
	sess := session.Must(session.NewSession())
	mskapi := MSKclient{Client: kafka.New(sess)}
	ec2api := Ec2Client{Client: ec2.New(sess)}
	config := ClusterConfig{
		Name:           configName,
		Description:    "sample desc",
		Kafka_Versions: []string{"1.1.1", "2.2.1"},
	}

	t.Run("vpcCidrCheck", func(t *testing.T) {
		ctx := context.Background()
		cidr, err := ec2api.vpcCidr(ctx, vpcId)
		assert.Nil(t, err)
		assert.NotZero(t, cidr)

		//second check
		cidr, err = ec2api.vpcCidr(ctx, vpcId)
		assert.Nil(t, err)
		assert.NotZero(t, cidr)

	})

	t.Run("checkMSKClusterConfig", func(t *testing.T) {
		ctx := context.Background()
		configArn, err := mskapi.createConfig(ctx, config, serverProp)
		assert.Nil(t, err)
		assert.NotZero(t, configArn)

		configArn, err = mskapi.createConfig(ctx, config, serverProp)
		assert.Nil(t, err)
		assert.NotZero(t, configArn)
	})

	t.Run("getPrivateSubnets", func(t *testing.T) {
		ctx := context.Background()
		privSubnets, err := ec2api.privSubnets(ctx, vpcId)
		assert.Nil(t, err)
		assert.NotZero(t, privSubnets)

		//second check
		privSubnets, err = ec2api.privSubnets(ctx, vpcId)
		assert.Nil(t, err)
		assert.NotZero(t, privSubnets)
	})
}
