package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
	"github.com/aws/aws-sdk-go/service/elasticsearchservice"
	"github.com/aws/aws-sdk-go/service/elasticsearchservice/elasticsearchserviceiface"
	"log"
)

//elasticsearch client
type esclient struct {
	Client elasticsearchserviceiface.ElasticsearchServiceAPI
}

//cloudwatch logs client
type cwlogs struct {
	Client cloudwatchlogsiface.CloudWatchLogsAPI
}

//elasticsearch config struct as input
type esDomainConfig struct {
	Domain               string //the name of the es domain
	DomainArn            string //the arn of the es domain
	IndexSlowLogsArn     string //the arn of the indexslow cloudwatch logs arn for es
	SearchSlowLogsArn    string //the arn of the searchslow cloudwatch logs arn for es
	ESApplicationLogsArn string //the arn of application error cloudwatch logs arn for es
}

//a sample iam policy struct
type policyDocument struct {
	Version   string           //version of the policy
	Statement []statementEntry // a list of iam policy statements
}

//iam policy principal for resource based policies
type principal struct {
	Service string //principal who needs access to cloudwatch logs, in this case happens to be elasticsearch service
}

//iam policy statement
type statementEntry struct {
	Effect    string    //effect of the policy : ALLOW/DENY
	Principal principal //principal struct
	Action    []string  //a set of actions that the principal is allows to perform on the resource
	Resource  string    // the resource to which the permission is being granted.
}

//resource policy input struct
type resPolicyInput struct {
	policyName string //the name of the policy. A consistent scheme. Useful while deleting the policy
	logKind    string //the king of the ES logs the policy caters to.
}

const (
	SEARCHSLOWLOGS = "Search-logs"      //string used to create policy name for search logs.
	INDEXSLOWLOGS  = "Index-logs"       //string used to create policy name for index logs.
	ESAPPLOGS      = "Application-logs" //string used to create policy name for application error logs.
)

//create a resource based iam policy that allows access to cloudwatch logs. Accessing entity is elastic search.
func (c *cwlogs) resourcePolicy(ctx context.Context, config esDomainConfig, policyConfig resPolicyInput) error {

	var resource string
	if policyConfig.logKind == SEARCHSLOWLOGS {
		resource = config.SearchSlowLogsArn
	} else if policyConfig.logKind == INDEXSLOWLOGS {
		resource = config.IndexSlowLogsArn
	} else if policyConfig.logKind == ESAPPLOGS {
		resource = config.ESApplicationLogsArn
	} else {
		return fmt.Errorf("invalid log type")
	}

	policy := policyDocument{
		Version: "2012-10-17",
		Statement: []statementEntry{{
			Effect: "Allow",
			Principal: principal{
				Service: "es.amazonaws.com",
			},
			Action: []string{
				"logs:PutLogEvents",
				"logs:CreateLogStream",
			},
			Resource: resource,
		},
		},
	}

	b, err := json.Marshal(&policy)
	if err != nil {
		return fmt.Errorf("Error marshaling policy : %v", err)
	}

	param := cloudwatchlogs.PutResourcePolicyInput{
		PolicyName:     aws.String(policyConfig.policyName),
		PolicyDocument: aws.String(string(b)),
	}

	_, err = c.Client.PutResourcePolicyWithContext(ctx, &param)
	if err != nil {
		return fmt.Errorf("unable to create a resource policy for ES access to cloudwatch logs %s : %v", resource, err)
	}
	return nil
}

//delete Resource based iam policy for access to cloudwatch logs. Accessing entity is elastic search service.
//to be invoked during cloudformation delete stack operation so that resource policies are not found orphan afte stack is deleted.
func (c *cwlogs) delResourcePolicy(ctx context.Context, policyName string) error {
	param := cloudwatchlogs.DeleteResourcePolicyInput{
		PolicyName: aws.String(policyName),
	}

	_, err := c.Client.DeleteResourcePolicyWithContext(ctx, &param)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "ResourceNotFoundException":
				return nil
			default:
				return fmt.Errorf("unable to delete resource policy %s: %v", policyName, err)
			}
		}
	}
	return nil
}

//update Elasticsearch domain with LogPublishOptions configuration. Currently (Oct-19) not supported by CloudFormation
//The logs for elasticsearch are disabled by default.
//If enabled, they need to be supplied with the Arns of the cloudwatch logs.
func (e *esclient) publishLog(ctx context.Context, config esDomainConfig) (string, error) {
	indexSlow := elasticsearchservice.LogPublishingOption{
		Enabled:                   aws.Bool(true),
		CloudWatchLogsLogGroupArn: aws.String(config.IndexSlowLogsArn),
	}

	searchSlow := elasticsearchservice.LogPublishingOption{
		Enabled:                   aws.Bool(true),
		CloudWatchLogsLogGroupArn: aws.String(config.SearchSlowLogsArn),
	}

	appLogs := elasticsearchservice.LogPublishingOption{
		Enabled:                   aws.Bool(true),
		CloudWatchLogsLogGroupArn: aws.String(config.ESApplicationLogsArn),
	}

	param := elasticsearchservice.UpdateElasticsearchDomainConfigInput{
		DomainName: aws.String(config.Domain),
		LogPublishingOptions: map[string]*elasticsearchservice.LogPublishingOption{
			"INDEX_SLOW_LOGS":     &indexSlow,
			"SEARCH_SLOW_LOGS":    &searchSlow,
			"ES_APPLICATION_LOGS": &appLogs,
		},
	}

	out, err := e.Client.UpdateElasticsearchDomainConfigWithContext(ctx, &param)
	if err != nil {
		return "", fmt.Errorf("Error occurred while creating a log publish option: %v", err)
	}

	return aws.StringValue(out.DomainConfig.LogPublishingOptions.Status.State), nil
}

//Disables Elasticsearch logs that were enabled.
//invoked during cloudformation delete stack operation.
func (e *esclient) deletePublishLog(ctx context.Context, config esDomainConfig) error {

	indexSlow := elasticsearchservice.LogPublishingOption{
		Enabled: aws.Bool(false),
	}

	searchSlow := elasticsearchservice.LogPublishingOption{
		Enabled: aws.Bool(false),
	}

	appLogs := elasticsearchservice.LogPublishingOption{
		Enabled: aws.Bool(false),
	}

	param := elasticsearchservice.UpdateElasticsearchDomainConfigInput{
		DomainName: aws.String(config.Domain),
		LogPublishingOptions: map[string]*elasticsearchservice.LogPublishingOption{
			"INDEX_SLOW_LOGS":     &indexSlow,
			"SEARCH_SLOW_LOGS":    &searchSlow,
			"ES_APPLICATION_LOGS": &appLogs,
		},
	}

	_, err := e.Client.UpdateElasticsearchDomainConfigWithContext(ctx, &param)
	if err != nil {
		return fmt.Errorf("Unable to disable publish logs in DELETE operation of the stack : %+v", err)
	}
	return nil
}

func publishLog(ctx context.Context, event cfn.Event) (physicalResourceId string, data map[string]interface{}, err error) {
	log.Println("Initializing...")
	sess := session.Must(session.NewSession())
	esApi := esclient{Client: elasticsearchservice.New(sess)}
	cwApi := cwlogs{Client: cloudwatchlogs.New(sess)}
	config := esDomainConfig{
		Domain:               event.ResourceProperties["DomainName"].(string),
		IndexSlowLogsArn:     event.ResourceProperties["IndexSlowLogsArn"].(string),
		SearchSlowLogsArn:    event.ResourceProperties["SearchSlowLogsArn"].(string),
		ESApplicationLogsArn: event.ResourceProperties["ESApplicationLogsArn"].(string),
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

	switch event.RequestType {
	case cfn.RequestCreate:
		log.Println("CREATE: starting the create operation ")
		log.Printf("event data :%+v\n", event)
		log.Printf("Config Object : %+v\n", config)

		//creating index slow logs resource policy
		err := cwApi.resourcePolicy(ctx, config, indexSlowLogInput)
		if err != nil {
			return "", nil, err
		}
		//creating search slow logs resource policy
		err = cwApi.resourcePolicy(ctx, config, searchSlowLogInput)
		if err != nil {
			return "", nil, err
		}
		//creating application error logs resource policy
		err = cwApi.resourcePolicy(ctx, config, esAppLogInput)
		if err != nil {
			return "", nil, err
		}

		resp, err := esApi.publishLog(ctx, config)
		if err != nil {
			return "", nil, err
		}
		data = map[string]interface{}{
			"LogPublishingOptionsStatus": resp,
		}
		return "", data, nil

	case cfn.RequestUpdate:
		log.Println("UPDATE: starting the update operation ")
		log.Printf("event data : %+v\n", event)
		event.RequestType = cfn.RequestUpdate
		return publishLog(ctx, event)

	case cfn.RequestDelete:
		log.Println("DELETE: starting the delete operation")
		log.Printf("event data : %+v\n", event)

		//delete index slow logs resource policy
		err := cwApi.delResourcePolicy(ctx, indexSlowLogInput.policyName)
		if err != nil {
			return "", nil, err
		}

		//delete search logs resource policy
		err = cwApi.delResourcePolicy(ctx, searchSlowLogInput.policyName)
		if err != nil {
			return "", nil, err
		}

		//delete application error logs resource policy
		err = cwApi.delResourcePolicy(ctx, esAppLogInput.policyName)
		if err != nil {
			return "", nil, err
		}

		//disable publish logs configuration
		err = esApi.deletePublishLog(ctx, config)
		if err != nil {
			return "", nil, err
		}
		return "", nil, nil
	}
	return
}

//expected Input
//{
//	"DomainName":"The name of the elasticsearch domain",
//	"IndexSlowLogsArn":"The arn of the cloudwatch logs to be used to store index logs of the ES domain",
//	"SearchSlowLogsArn":"The arn of the cloudwatch logs to be used to store search logs of the ES domain",
//	"ESApplicationLogsArn":"The arn of the cloudwatch logs to be used to store application error logs of the ES domain"
//}

//expected output
//{
//	"LogPublishingOptionsStatus":"The status of the log publishing options."
//}
func main() {
	lambda.Start(cfn.LambdaWrap(publishLog))
}
