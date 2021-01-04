package main

import (
	"context"
	"fmt"
	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/kafka"
	"github.com/aws/aws-sdk-go/service/kafka/kafkaiface"
	"log"
	"strings"
)

type ClusterConfig struct {
	Name           string   `json:"name"`           //name of the cluster configuration
	Description    string   `json:"description"`    //description of the config
	Kafka_Versions []string `json:"kafka-versions"` // a list of MSK versions : currently available are 1.1.1 and 2.2.1
}

//MSK service client
type MSKclient struct {
	Client kafkaiface.KafkaAPI
}

//EC2 service client
type Ec2Client struct {
	Client ec2iface.EC2API
}

//If the cluster config exists already, returns the arn of the cluster config that matches the name
// supplied and revision being 1
func (k *MSKclient) listConfigurations(ctx context.Context, name string) (string, error) {

	out, err := k.Client.ListConfigurationsWithContext(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("Unable to list MSK cluster configurations : %v", err)
	}

	for _, config := range out.Configurations {

		if name == aws.StringValue(config.Name) && aws.Int64Value(config.LatestRevision.Revision) == 1 {
			log.Printf("Matching MSK cluster configuration found : %v", aws.StringValue(config.Arn))
			return aws.StringValue(config.Arn), nil
		} else {
			log.Printf("configuration %s is not a match", aws.StringValue(config.Arn))
		}
	}

	return "", fmt.Errorf("Unable to list MSK cluster configuration by name %s", name)
}

//create the configuration for MSK cluster to use
func (k *MSKclient) createConfig(ctx context.Context, conf ClusterConfig, serverProps []byte) (string, error) {

	param := kafka.CreateConfigurationInput{
		Description:      aws.String(conf.Description),
		KafkaVersions:    aws.StringSlice(conf.Kafka_Versions),
		Name:             aws.String(conf.Name),
		ServerProperties: serverProps,
	}

	out, err := k.Client.CreateConfigurationWithContext(ctx, &param)
	if err != nil {
		log.Println("cannot create the new configuration: ", err)
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case kafka.ErrCodeConflictException:
				return k.listConfigurations(ctx, conf.Name)
			default:
				return "", fmt.Errorf("unable to create cluster configuration: %v", aerr.Message())
			}
		} else {
			return "", fmt.Errorf("unable to create cluster configuration :%v", err)
		}
	}

	log.Println("kafka cluster  configuration arn is :", aws.StringValue(out.Arn))
	return aws.StringValue(out.Arn), nil
}

//fetches the cidr of the VPC using the given vpc. So that it could be used in places
// such as SecurityGroups in cloudformation template etc.
func (e *Ec2Client) vpcCidr(ctx context.Context, vpcId string) (string, error) {

	param := ec2.DescribeVpcsInput{VpcIds: aws.StringSlice([]string{vpcId})}
	out, err := e.Client.DescribeVpcsWithContext(ctx, &param)
	if err != nil {
		return "", fmt.Errorf("unable to describe VPC : %v", err)
	}

	for _, vpc := range out.Vpcs {
		if vpcId == aws.StringValue(vpc.VpcId) {
			return aws.StringValue(vpc.CidrBlock), nil
		} else {
			return "", fmt.Errorf("vpc matching the ID %s could not be found", vpcId)
		}
	}
	return "", nil

}

//returns a list of private subnets associated with the vpc.
//the lambda function (custom resource) that will perform post processing task will need to be allocated to
//the subnets that have NAT translation even though the MSK is being created is in public subnet.
func (c *Ec2Client) privSubnets(ctx context.Context, vpcId string) ([]string, error) {
	param := ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String(vpcId)},
			},
			&ec2.Filter{
				Name:   aws.String("route.nat-gateway-id"),
				Values: []*string{aws.String("nat*")},
			},
		},
	}

	out, err := c.Client.DescribeRouteTablesWithContext(ctx, &param)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return nil, fmt.Errorf("unable to list route destinations: %s", aerr.Message())
			}
		} else {
			return nil, fmt.Errorf("unable to list route destinations: %v", err)
		}
	}

	var privSubnets []string
	for _, routetable := range out.RouteTables {
		for _, association := range routetable.Associations {
			privSubnets = append(privSubnets, aws.StringValue(association.SubnetId))
		}
	}
	log.Printf("List of private subnets is :%v. Length : %v", privSubnets, len(privSubnets))
	return privSubnets, nil
}

func configureCluster(ctx context.Context, event cfn.Event) (physicalResourceId string, data map[string]interface{}, err error) {

	log.Println("Initializing....")
	sess, err := session.NewSession()
	if err != nil {
		return "", nil, fmt.Errorf("unable to create a new session: %v", err)
	}
	mskApi := MSKclient{Client: kafka.New(sess)}
	ec2Api := Ec2Client{Client: ec2.New(sess)}
	vpcId := event.ResourceProperties["VpcId"].(string)
	var config ClusterConfig
	var serverProps string

	switch event.RequestType {
	case cfn.RequestCreate:

		log.Println("CREATE: creating MSK cluster configuration.")
		log.Printf("event is :%+v\n", event)

		cidr, err := ec2Api.vpcCidr(ctx, vpcId)
		if err != nil {
			return "", nil, err
		}
		log.Printf("cidr is :%s", cidr)

		privSubs, err := ec2Api.privSubnets(ctx, vpcId)
		if err != nil {
			return "", nil, err
		}

		if conf, ok := event.ResourceProperties["ClusterConfig"].(map[string]interface{}); ok {
			config.Name = conf["Name"].(string)
			config.Description = conf["Description"].(string)
			tempVers := conf["Kafka_Versions"].([]interface{})
			for _, tempVer := range tempVers {
				config.Kafka_Versions = append(config.Kafka_Versions, tempVer.(string))
			}

		} else {
			return "", nil, fmt.Errorf("unable to convert event data to ClusterConfig")
		}

		if props, ok := event.ResourceProperties["ServerProperties"].([]interface{}); ok {
			for _, prop := range props {
				serverProps = serverProps + prop.(string) + "\n"
			}
		} else {
			return "", nil, fmt.Errorf("unable to convert server properties to an array of string")
		}

		configArn, err := mskApi.createConfig(ctx, config, []byte(serverProps))
		if err != nil {
			return "", nil, fmt.Errorf("Unable to create MSK cluster config : %v", err)
		}

		var r []string
		for _, subnet := range privSubs {
			if subnet != "" {
				r = append(r, subnet)
			}
		}

		data = map[string]interface{}{
			"ConfigurationArn": configArn,
			"VpcCidr":          cidr,
			"PrivateSubnets":   strings.Join(r, ","),
		}
		log.Printf("data being returned is :%+v", data)
		return configArn, data, nil

	case cfn.RequestUpdate:
		log.Println("UPDATE: update operation is not supported. Taking a clean exit...")
		return
	case cfn.RequestDelete:
		log.Println("DELETE: delete operation is not supported. Taking a clean exit...")
		return
	}
	return
}

//The lambda takes the following sample input under ResourceProperties
//{
// "VpcId":"vpc-23l4j2l3kj4"
// "ClusterConfig":{ "Name":"mycustomconfig","Description":"sample desc","Kafka_Versions:["1.1.1","2.2.1"]}
// }

//the lambda function intends to return the following sample object as its response once it executes successfully.
// {
//	 "ConfigurationArn":"arn:aws:kafka:us-west-2:508718283261:configuration/krunal1/25815693-f755-47f3-873b-aaeb92dc25d8-3"
//	 "VpcCidr":"192.168.0.0/24"
//   "PrivateSubnets":["subnet-031fdba3fb3045c3f","subnet-0b688e70c6c723bf3","subnet-0637a832d0ca08d3f"]
// }
//ConfigurationArn is to be used while creating an MSK cluster.
//VpcCidr is to be used conditionally to create a security group with ingress rules with cidr as the source range.
//PrivateSubnets is to be used with the Custom Resource lambda function that will need private acccess to MSK cluster.
func main() {
	lambda.Start(cfn.LambdaWrap(configureCluster))
}
