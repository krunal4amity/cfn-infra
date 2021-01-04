package main

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
	"github.com/aws/aws-sdk-go/service/elasticsearchservice"
	"github.com/aws/aws-sdk-go/service/elasticsearchservice/elasticsearchserviceiface"
	"github.com/tj/assert"
	"os"
	"testing"
)

var (
	key          = os.Getenv("AWS_ACCESS_KEY_ID")
	pass         = os.Getenv("AWS_SECRET_ACCESS_KEY")
	domainName   = "mlsdataplatform-qa"
	appLogArn    = "arn:aws:logs:us-west-2:508718283261:log-group:mls/aes/domains/mlsdataplatform-qa/application-logs:*"
	indexLogArn  = "arn:aws:logs:us-west-2:508718283261:log-group:mls/aes/domains/mlsdataplatform-qa/index-logs:*"
	searchLogArn = "arn:aws:logs:us-west-2:508718283261:log-group:mls/aes/domains/mlsdataplatform-qa/search-logs:*"
)

type mockCWPutResourcePolicy struct {
	cloudwatchlogsiface.CloudWatchLogsAPI
	Resp cloudwatchlogs.PutResourcePolicyOutput
}

type mockCWDeleteResourcePolicy struct {
	cloudwatchlogsiface.CloudWatchLogsAPI
	Resp cloudwatchlogs.DeleteResourcePolicyOutput
}

type mockESUpdateDomainConfig struct {
	elasticsearchserviceiface.ElasticsearchServiceAPI
	Resp elasticsearchservice.UpdateElasticsearchDomainConfigOutput
}

func (m *mockCWPutResourcePolicy) PutResourcePolicyWithContext(ctx aws.Context, param *cloudwatchlogs.PutResourcePolicyInput, opts ...request.Option) (*cloudwatchlogs.PutResourcePolicyOutput, error) {
	return &m.Resp, nil
}

func (m *mockCWDeleteResourcePolicy) DeleteResourcePolicyWithContext(ctx aws.Context, param *cloudwatchlogs.DeleteResourcePolicyInput, opts ...request.Option) (*cloudwatchlogs.DeleteResourcePolicyOutput, error) {
	return &m.Resp, nil
}

func (m *mockESUpdateDomainConfig) UpdateElasticsearchDomainConfigWithContext(ctx aws.Context, param *elasticsearchservice.UpdateElasticsearchDomainConfigInput, opts ...request.Option) (*elasticsearchservice.UpdateElasticsearchDomainConfigOutput, error) {
	return &m.Resp, nil
}

func Test_MockPutResourcePolicy(t *testing.T) {
	cases := []struct {
		Resp     cloudwatchlogs.PutResourcePolicyOutput
		Expected cloudwatchlogs.ResourcePolicy
	}{
		{
			Resp: cloudwatchlogs.PutResourcePolicyOutput{
				ResourcePolicy: &cloudwatchlogs.ResourcePolicy{
					PolicyName: aws.String("MLSPolicy1"),
				},
			},
			Expected: cloudwatchlogs.ResourcePolicy{
				PolicyName: aws.String("MLSPolicy1"),
			},
		},
	}

	for _, c := range cases {
		cwApi := cwlogs{
			Client: &mockCWPutResourcePolicy{Resp: c.Resp},
		}
		err := cwApi.resourcePolicy(context.Background(), esDomainConfig{domainName, "", indexLogArn, searchLogArn, appLogArn}, resPolicyInput{"MLSPolicy1", INDEXSLOWLOGS})
		assert.Nil(t, err)
	}
}

func Test_MockDeleteResourcePolicy(t *testing.T) {
	cases := []struct {
		Resp     cloudwatchlogs.DeleteResourcePolicyOutput
		Expected string
	}{
		{
			Resp:     cloudwatchlogs.DeleteResourcePolicyOutput{},
			Expected: "deleted",
		},
	}

	for _, c := range cases {
		cwApi := cwlogs{
			Client: &mockCWDeleteResourcePolicy{Resp: c.Resp},
		}
		err := cwApi.delResourcePolicy(context.Background(), "MLSPolicy1")
		assert.Nil(t, err)
	}
}

func Test_MockPublishLog(t *testing.T) {
	cases := []struct {
		Resp     elasticsearchservice.UpdateElasticsearchDomainConfigOutput
		Expected elasticsearchservice.OptionStatus
	}{
		{
			Resp: elasticsearchservice.UpdateElasticsearchDomainConfigOutput{
				DomainConfig: &elasticsearchservice.ElasticsearchDomainConfig{
					LogPublishingOptions: &elasticsearchservice.LogPublishingOptionsStatus{
						Status: &elasticsearchservice.OptionStatus{
							State: aws.String("good"),
						},
					},
				},
			},
			Expected: elasticsearchservice.OptionStatus{
				State: aws.String("good"),
			},
		},
	}

	for _, c := range cases {
		esApi := esclient{
			Client: &mockESUpdateDomainConfig{Resp: c.Resp},
		}
		status, err := esApi.publishLog(context.Background(), esDomainConfig{domainName, "", indexLogArn, searchLogArn, appLogArn})
		assert.Nil(t, err)
		t.Log("log publish status: ", status)
		assert.Equal(t, status, aws.StringValue(c.Expected.State))

		err = esApi.deletePublishLog(context.Background(), esDomainConfig{domainName, "", indexLogArn, searchLogArn, appLogArn})
		assert.Nil(t, err)
	}
}

func Test_EnablePublishLog(t *testing.T) {
	if key == "" || pass == "" {
		t.SkipNow()
	}
	sess := session.Must(session.NewSession())
	ctx := context.Background()
	cwApi := cwlogs{Client: cloudwatchlogs.New(sess)}
	esApi := esclient{Client: elasticsearchservice.New(sess)}
	config := esDomainConfig{
		Domain:               domainName,
		IndexSlowLogsArn:     indexLogArn,
		SearchSlowLogsArn:    searchLogArn,
		ESApplicationLogsArn: appLogArn,
	}
	indexSlowLogInput := resPolicyInput{
		policyName: fmt.Sprintf("AES-" + config.Domain + "-" + INDEXSLOWLOGS),
		logKind:    INDEXSLOWLOGS,
	}
	searchSlowLogInput := resPolicyInput{
		policyName: fmt.Sprintf("AES-" + config.Domain + "-" + SEARCHSLOWLOGS),
		logKind:    SEARCHSLOWLOGS,
	}
	esAppLogInput := resPolicyInput{
		policyName: fmt.Sprintf("AES-" + config.Domain + "-" + ESAPPLOGS),
		logKind:    ESAPPLOGS,
	}
	t.Run("createResourcePolicy", func(t *testing.T) {
		err := cwApi.resourcePolicy(ctx, config, indexSlowLogInput)
		assert.Nil(t, err)
		//again
		err = cwApi.resourcePolicy(ctx, config, indexSlowLogInput)
		assert.Nil(t, err)

		err = cwApi.resourcePolicy(ctx, config, searchSlowLogInput)
		assert.Nil(t, err)
		//again
		err = cwApi.resourcePolicy(ctx, config, searchSlowLogInput)
		assert.Nil(t, err)

		err = cwApi.resourcePolicy(ctx, config, esAppLogInput)
		assert.Nil(t, err)
		//again
		err = cwApi.resourcePolicy(ctx, config, esAppLogInput)
		assert.Nil(t, err)
	})

	t.Run("PublishLog", func(t *testing.T) {
		out, err := esApi.publishLog(ctx, config)
		assert.Nil(t, err)
		assert.NotZero(t, out)
		t.Log(out)
		//again
		out, err = esApi.publishLog(ctx, config)
		assert.Nil(t, err)
		assert.NotZero(t, out)
	})

	t.Run("disableLogPublish", func(t *testing.T) {
		err := esApi.deletePublishLog(ctx, config)
		assert.Nil(t, err)
		//again
		err = esApi.deletePublishLog(ctx, config)
		assert.Nil(t, err)
	})

	t.Run("deleteResourcePolicy", func(t *testing.T) {
		err := cwApi.delResourcePolicy(ctx, indexSlowLogInput.policyName)
		assert.Nil(t, err)
		//again
		err = cwApi.delResourcePolicy(ctx, indexSlowLogInput.policyName)
		assert.Nil(t, err)

		err = cwApi.delResourcePolicy(ctx, searchSlowLogInput.policyName)
		assert.Nil(t, err)
		//again
		err = cwApi.delResourcePolicy(ctx, searchSlowLogInput.policyName)
		assert.Nil(t, err)

		err = cwApi.delResourcePolicy(ctx, esAppLogInput.policyName)
		assert.Nil(t, err)
		//again
		err = cwApi.delResourcePolicy(ctx, esAppLogInput.policyName)
		assert.Nil(t, err)
	})

}
