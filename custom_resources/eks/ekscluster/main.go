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
	"github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/eks/eksiface"
	"log"
	"reflect"
	"strconv"
	"time"
)

//EKS client to perform eks related operations.
type EksClient struct {
	Client eksiface.EKSAPI
}

//EC2 client to perform EC2 related oeprations.
type Ec2Client struct {
	Client ec2iface.EC2API
}

//custom struct for managing eks cluster
type EksClusterConfig struct {
	Name                  string   //cluster name
	RoleArn               string   //EKS cluster role's arn
	Version               string   //version of the kubernetes
	PrivateSubnets        []string //list of private subnet ids
	PublicSubnets         []string //list of public subnet ids
	SecurityGroupIds      []string //list of security groups ids required to be added to eks cluster.
	EndpointPublicAccess  bool
	EndpointPrivateAccess bool
}

//custom output struct of eks cluster
type EksClusterOutput struct {
	Arn                      string   //eks cluster arn
	Endpoint                 string   //eks api server endpoint.
	CertificateAuthorityData string   //CA data of the eks cluster
	OidcIssuer               string   //OIDC issuer of the eks cluster
	SecGroup                 []string //Security Group IDs attached to the cluster.
	Subnets                  []string //List of subnets to be used by the eks cluster.
	Version                  string   //Kubernetes version
}

//adds tags required as per https://docs.aws.amazon.com/eks/latest/userguide/alb-ingress.html
func (e *Ec2Client) addElbIngressTags(ctx context.Context, config EksClusterConfig) error {

	commonTag := fmt.Sprintf("kubernetes.io/cluster/%s", config.Name)

	var privateSubnetTags = map[string]string{
		commonTag:                         "shared",
		"kubernetes.io/role/internal-elb": "1",
	}

	var publicSubnetTags = map[string]string{
		commonTag:                "shared",
		"kubernetes.io/role/elb": "1",
	}

	for _, publicSubnet := range config.PublicSubnets {

		var tags []*ec2.Tag
		for k, v := range publicSubnetTags {
			tag := &ec2.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			}
			tags = append(tags, tag)
		}
		tagsInput := &ec2.CreateTagsInput{
			Tags:      tags,
			Resources: []*string{aws.String(publicSubnet)},
		}

		_, err := e.Client.CreateTags(tagsInput)
		if err != nil {

			return fmt.Errorf("unable to create tags for subnet %s: %v", publicSubnet, err)
		}
	}

	for _, privatesubnet := range config.PrivateSubnets {

		var tags []*ec2.Tag
		for k, v := range privateSubnetTags {
			tag := &ec2.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			}
			tags = append(tags, tag)
		}
		tagsInput := &ec2.CreateTagsInput{
			Tags:      tags,
			Resources: []*string{aws.String(privatesubnet)},
		}
		_, err := e.Client.CreateTags(tagsInput)
		if err != nil {
			return fmt.Errorf("unable to create tags for subnet %s: %v", privatesubnet, err)
		}
	}

	return nil
}

//removes tags added as per https://docs.aws.amazon.com/eks/latest/userguide/alb-ingress.html during stack deletion
//so as not to cause confusion or conflict with any other subsequent eks clusters to be created with different names.
//deletes only the tag "kubernetes.io/cluster/<cluster-name>:shared". The others are left as is, since they might
//have been created by other eks cluster and may still be in use.
func (e *Ec2Client) removeElbIngressTags(ctx context.Context, config EksClusterConfig) error {

	commonTagKey := fmt.Sprintf("kubernetes.io/cluster/%s", config.Name)

	commonTag := &ec2.Tag{
		Key:   aws.String(commonTagKey),
		Value: aws.String("shared"),
	}

	for _, publicSubnet := range config.PublicSubnets {

		var tags []*ec2.Tag
		tags = append(tags, commonTag)

		tagsInput := &ec2.DeleteTagsInput{
			Tags:      tags,
			Resources: []*string{aws.String(publicSubnet)},
		}
		_, err := e.Client.DeleteTags(tagsInput)
		if err != nil {
			return fmt.Errorf("unable to create tags for subnet %s: %v", publicSubnet, err)
		}
	}

	for _, privatesubnet := range config.PrivateSubnets {

		var tags []*ec2.Tag
		tags = append(tags, commonTag)
		tagsInput := &ec2.DeleteTagsInput{
			Tags:      tags,
			Resources: []*string{aws.String(privatesubnet)},
		}
		_, err := e.Client.DeleteTags(tagsInput)
		if err != nil {
			return fmt.Errorf("unable to create tags for subnet %s: %v", privatesubnet, err)
		}
	}

	return nil
}

//deletes eks cluster using the given eks cluster name
func (e *EksClient) deleteCluster(ctx context.Context, clusterName string) error {
	input := eks.DeleteClusterInput{Name: aws.String(clusterName)}
	_, err := e.Client.DeleteClusterWithContext(ctx, &input)
	if err != nil {
		return fmt.Errorf("Error occurred while deleting eks cluster : %v", err)
	}
	return nil
}

//upgrade eks kubernetes version if required
func (e *EksClient) upgradeVersion(ctx context.Context, config EksClusterConfig) error {
	input := eks.UpdateClusterVersionInput{Name: aws.String(config.Name), Version: aws.String(config.Version)}
	_, err := e.Client.UpdateClusterVersionWithContext(ctx, &input)
	if err != nil {
		return fmt.Errorf("Error occurred while upgrading the version of EKS cluster : %v", err)
	}
	return nil
}

//upgrades eks cluster other configuration
func (e *EksClient) upgradeConfig(ctx context.Context, config EksClusterConfig) error {

	allSubnets := append(config.PrivateSubnets, config.PublicSubnets...)

	input := eks.UpdateClusterConfigInput{
		ClientRequestToken: aws.String(time.Now().String()),
		Name:               aws.String(config.Name),
		ResourcesVpcConfig: &eks.VpcConfigRequest{
			SubnetIds:             aws.StringSlice(allSubnets),
			SecurityGroupIds:      aws.StringSlice(config.SecurityGroupIds),
			EndpointPublicAccess:  aws.Bool(config.EndpointPublicAccess),
			EndpointPrivateAccess: aws.Bool(config.EndpointPrivateAccess),
		},
	}

	_, err := e.Client.UpdateClusterConfigWithContext(ctx, &input)
	if err != nil {
		return fmt.Errorf("unable to update eks cluster configuration :%v", err)
	}
	return nil
}

//Gets existing cluster.
func (e *EksClient) getCluster(ctx context.Context, clusterName string) (EksClusterOutput, error) {
	input := eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	}

	out, err := e.Client.DescribeClusterWithContext(ctx, &input)
	if err != nil {
		return EksClusterOutput{}, fmt.Errorf("Unable to describe the cluster %s", clusterName)
	}

	output := EksClusterOutput{
		Arn:                      aws.StringValue(out.Cluster.Arn),
		Endpoint:                 aws.StringValue(out.Cluster.Endpoint),
		CertificateAuthorityData: aws.StringValue(out.Cluster.CertificateAuthority.Data),
		OidcIssuer:               aws.StringValue(out.Cluster.Identity.Oidc.Issuer),
		Subnets:                  aws.StringValueSlice(out.Cluster.ResourcesVpcConfig.SubnetIds),
		SecGroup:                 aws.StringValueSlice(out.Cluster.ResourcesVpcConfig.SecurityGroupIds),
		Version:                  aws.StringValue(out.Cluster.Version),
	}
	return output, nil
}

//Creates cluster based on configuration parameters in the cloudformation template
func (e *EksClient) createCluster(ctx context.Context, config EksClusterConfig) (EksClusterOutput, error) {

	allSubnets := append(config.PublicSubnets, config.PrivateSubnets...)

	input := eks.CreateClusterInput{
		Name:    aws.String(config.Name),
		Version: aws.String(config.Version),
		RoleArn: aws.String(config.RoleArn),
		ResourcesVpcConfig: &eks.VpcConfigRequest{
			SecurityGroupIds:      aws.StringSlice(config.SecurityGroupIds),
			SubnetIds:             aws.StringSlice(allSubnets),
			EndpointPrivateAccess: aws.Bool(config.EndpointPrivateAccess),
			EndpointPublicAccess:  aws.Bool(config.EndpointPublicAccess),
		},
		Logging: &eks.Logging{
			ClusterLogging: []*eks.LogSetup{
				&eks.LogSetup{
					Enabled: aws.Bool(true),
					Types:   aws.StringSlice([]string{"api", "audit", "authenticator", "controllerManager", "scheduler"}),
				},
			},
		},
		ClientRequestToken: aws.String(time.Now().String()),
	}

	out, err := e.Client.CreateClusterWithContext(ctx, &input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case eks.ErrCodeResourceInUseException:
				out, err := e.getCluster(ctx, config.Name)
				if err != nil {
					return EksClusterOutput{}, err
				}
				if !reflect.DeepEqual(out.Subnets, allSubnets) || !reflect.DeepEqual(out.SecGroup, config.SecurityGroupIds) {
					err := e.upgradeConfig(ctx, config)
					if err != nil {
						return EksClusterOutput{}, err
					}
				}
				if config.Version != out.Version {
					err := e.upgradeVersion(ctx, config)
					if err != nil {
						return EksClusterOutput{}, err
					}
				}
				return e.getCluster(ctx, config.Name)
			default:
				return EksClusterOutput{}, fmt.Errorf("unable to create the eks cluster: %s", aerr.Message())
			}
		} else {
			return EksClusterOutput{}, fmt.Errorf("unable to create the eks cluster - : %v", err)
		}
	}

	output := EksClusterOutput{
		Arn:                      aws.StringValue(out.Cluster.Arn),
		Endpoint:                 aws.StringValue(out.Cluster.Endpoint),
		CertificateAuthorityData: aws.StringValue(out.Cluster.CertificateAuthority.Data),
	}
	return output, nil
}

func manageEksCluster(ctx context.Context, event cfn.Event) (physicalResourceId string, data map[string]interface{}, err error) {
	log.Println("Initializing...")
	sess := session.Must(session.NewSession())
	eksApi := EksClient{Client: eks.New(sess)}
	ec2Api := Ec2Client{Client: ec2.New(sess)}
	var input EksClusterConfig

	if conf, ok := event.ResourceProperties["ClusterConfig"].(map[string]interface{}); ok {
		input.Name = conf["Name"].(string)
		input.Version = conf["Version"].(string)
		input.RoleArn = conf["RoleArn"].(string)
		pubAccess, err := strconv.ParseBool(conf["EndpointPublicAccess"].(string))
		if err != nil {
			return "", nil, fmt.Errorf("unable to parse to bool")
		}
		input.EndpointPublicAccess = pubAccess
		privAccess, err := strconv.ParseBool(conf["EndpointPrivateAccess"].(string))
		if err != nil {
			return "", nil, fmt.Errorf("unable to parse to bool")
		}
		input.EndpointPrivateAccess = privAccess
		tempPrivateSubnets := conf["PrivateSubnets"].([]interface{})
		tempPublicSubnets := conf["PublicSubnets"].([]interface{})
		tempSGs := conf["SecurityGroupIds"].([]interface{})

		for _, i := range tempPrivateSubnets {
			input.PrivateSubnets = append(input.PrivateSubnets, i.(string))
		}

		for _, i := range tempPublicSubnets {
			input.PublicSubnets = append(input.PublicSubnets, i.(string))
		}

		for _, j := range tempSGs {
			input.SecurityGroupIds = append(input.SecurityGroupIds, j.(string))
		}
	}

	switch event.RequestType {
	//Event Type: Create
	case cfn.RequestCreate:
		log.Println("CREATE: creating an EKS cluster")
		log.Printf("event is : %+v\n", event)

		//first create the tags required for alb ingress controller to be used on aws
		err := ec2Api.addElbIngressTags(ctx, input)
		if err != nil {
			return "", nil, err
		}

		log.Printf("input to create cluster method is : %v\n", input)
		out, err := eksApi.createCluster(ctx, input)
		if err != nil {
			return "", nil, err
		}

		data = map[string]interface{}{
			"Arn":                      out.Arn,
			"EndPoint":                 out.Endpoint,
			"CertificateAuthorityData": out.CertificateAuthorityData,
			"OidcIssuer":               out.OidcIssuer,
		}
		log.Printf("Data being return is : %v\n", data)

		return input.Name, data, nil //returns cluster name back, so that it can be used while deleting the cluster.

	//Event Type: Update.
	case cfn.RequestUpdate:
		log.Println("UPDATE: updating an EKS cluster")
		log.Printf("event is: %+v\n", event)
		event.RequestType = cfn.RequestCreate
		return manageEksCluster(ctx, event)

	//Event Type: Delete
	case cfn.RequestDelete:
		log.Println("DELETE: deleting an EKS cluster")
		err := ec2Api.removeElbIngressTags(ctx, input)
		if err != nil {
			return "", nil, err
		}
		err = eksApi.deleteCluster(ctx, event.PhysicalResourceID)
		if err != nil {
			return "", nil, err
		}
		return "", nil, nil
	}
	return "", nil, nil
}

//custom resource lambda function execution starts here.
func main() {
	lambda.Start(cfn.LambdaWrap(manageEksCluster))
}
