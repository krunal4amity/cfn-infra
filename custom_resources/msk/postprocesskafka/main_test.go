package main

import (
	"context"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kafka"
	"github.com/aws/aws-sdk-go/service/kafka/kafkaiface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"github.com/tj/assert"
	"os"
	"testing"
)

var (
	key        = os.Getenv("AWS_ACCESS_KEY_ID")
	pass       = os.Getenv("AWS_SECRET_ACCESS_KEY")
	clusterArn = "arn:aws:kafka:us-west-2:508718283261:cluster/mm/f68810de-4c55-44ad-929c-1fe3b91e4f6b-3"
	zoneID     = "Z2GEXWLLVCEQ3M"
)

type mockRoute53 struct {
	route53iface.Route53API
	hzResp route53.GetHostedZoneOutput
}

type mockMSK struct {
	kafkaiface.KafkaAPI
	bsResp kafka.GetBootstrapBrokersOutput
	clResp kafka.DescribeClusterOutput
}

func (m *mockRoute53) GetHostedZoneWithContext(aws.Context, *route53.GetHostedZoneInput, ...request.Option) (*route53.GetHostedZoneOutput, error) {
	return &m.hzResp, nil
}

func (m *mockMSK) GetBootstrapBrokersWithContext(aws.Context, *kafka.GetBootstrapBrokersInput, ...request.Option) (*kafka.GetBootstrapBrokersOutput, error) {
	return &m.bsResp, nil
}

func (m *mockMSK) DescribeClusterWithContext(aws.Context, *kafka.DescribeClusterInput, ...request.Option) (*kafka.DescribeClusterOutput, error) {
	return &m.clResp, nil
}

func Test_MockGetRecodSet(t *testing.T) {
	cases := []struct {
		Resp     route53.GetHostedZoneOutput
		Expected string
	}{
		{
			Resp: route53.GetHostedZoneOutput{
				HostedZone: &route53.HostedZone{Name: aws.String("simple.abc.com"), Id: aws.String(zoneID)},
				VPCs: []*route53.VPC{
					{
						VPCId:     aws.String("vpc-1234"),
						VPCRegion: aws.String("us-west-2"),
					},
				},
			},
			Expected: "simple.abc.com",
		},
	}

	for _, c := range cases {
		r53Api := R53client{Client: &mockRoute53{hzResp: c.Resp}}
		out, err := r53Api.recordSet(context.Background(), zoneID)
		assert.Nil(t, err)
		t.Log("MockGetRecordSet= domain name : ", out)
		assert.Equal(t, c.Expected, out)
	}
}

func Test_MockBrokerConString(t *testing.T) {
	cases := []struct {
		Resp     kafka.GetBootstrapBrokersOutput
		Expected string
	}{
		{
			Resp: kafka.GetBootstrapBrokersOutput{
				BootstrapBrokerStringTls: aws.String("10.1.1.1:9094"),
			},
			Expected: "10.1.1.1:9094",
		},
	}

	for _, c := range cases {
		mskApi := MSKclient{Client: &mockMSK{bsResp: c.Resp}}
		out, err := mskApi.brokerConString(context.Background(), "dummyArn")
		assert.Nil(t, err)
		t.Log("MockBrokerConString: ", out)
		assert.Equal(t, c.Expected, out)
	}
}

func Test_MockZookeerConString(t *testing.T) {
	cases := []struct {
		Resp     kafka.DescribeClusterOutput
		Expected string
	}{
		{
			Resp: kafka.DescribeClusterOutput{
				ClusterInfo: &kafka.ClusterInfo{
					ZookeeperConnectString: aws.String("10.1.1.1:2181"),
				},
			},
			Expected: "10.1.1.1:2181",
		},
	}

	for _, c := range cases {
		mskApi := MSKclient{Client: &mockMSK{clResp: c.Resp}}
		out, err := mskApi.zookeeperConString(context.Background(), "dummyArn")
		assert.Nil(t, err)
		t.Log("MockZookeererConString: ", out)
		assert.Equal(t, c.Expected, out)
	}
}

func TestAwsMethods(t *testing.T) {
	if key == "" || pass == "" {
		t.SkipNow()
	}
	sess := session.Must(session.NewSession())
	r53api := R53client{Client: route53.New(sess)}
	mskapi := MSKclient{Client: kafka.New(sess)}
	ctx := context.Background()
	t.Run("brokerlist", func(t *testing.T) {
		brokers, err := mskapi.brokerConString(ctx, clusterArn)
		t.Log(brokers)
		assert.Nil(t, err)
		assert.NotZero(t, brokers)
	})
	t.Run("zookeeperlist", func(t *testing.T) {
		zookeepers, err := mskapi.zookeeperConString(ctx, clusterArn)
		t.Log(zookeepers)
		assert.Nil(t, err)
		assert.NotZero(t, zookeepers)
	})
	t.Run("hostedZoneName", func(t *testing.T) {
		zoneName, err := r53api.recordSet(ctx, zoneID)
		t.Log(zoneName)
		assert.Nil(t, err)
		assert.NotZero(t, zoneName)
	})
}
