package main

import (
	"context"
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	msk "github.com/aws/aws-sdk-go/service/kafka"
	"github.com/aws/aws-sdk-go/service/kafka/kafkaiface"
	r53 "github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

//kafka topic configuration struct
type kafkaTopicConfig struct {
	Name              string //name of the topic
	ReplicationFactor int    //replication factor
	NumOfPartitions   int    //number of partitions
}

//Route 53 client
type R53client struct {
	Client route53iface.Route53API
}

//MSK client
type MSKclient struct {
	Client kafkaiface.KafkaAPI
}

//Apache Kafka client. However it doesn't include any 'sarama' elements
type Kafka struct {
	BrokerConn       string
	Topics           []kafkaTopicConfig
	BrokerController string
}

//some error codes that we care about. Available at https://kafka.apache.org/protocol#protocol_error_codes
const (
	NOT_CONTROLLER       = 41 //if the broker being used is not a controller
	TOPIC_ALREADY_EXISTS = 36 //if the topic already exists
	SUCCESS              = 0  //if create topic operation executes fine and doesn't result in any error code.
)

//fetches the hosted zone name based on the hosted zone ID supplied. To be used in case it is required to create a route53 record set.
func (r *R53client) recordSet(ctx context.Context, zoneId string) (string, error) {

	param := r53.GetHostedZoneInput{Id: aws.String(zoneId)}

	out, err := r.Client.GetHostedZoneWithContext(ctx, &param)
	if err != nil {
		return "", fmt.Errorf("unable to get hosted zone : %v", err)
	}

	log.Printf("Hosted Zone Name :%s", aws.StringValue(out.HostedZone.Name))
	return aws.StringValue(out.HostedZone.Name), nil
}

//finds out existing bootstrap broker connection string from describeCluster operation on MSK
func (m *MSKclient) brokerConString(ctx context.Context, clusterArn string) (string, error) {

	param := msk.GetBootstrapBrokersInput{
		ClusterArn: aws.String(clusterArn),
	}

	out, err := m.Client.GetBootstrapBrokersWithContext(ctx, &param)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return "", fmt.Errorf("unable to describe brokerlist: %s-%v", aerr.Code(), aerr.Message())
			}
		} else {
			return "", fmt.Errorf("unable to describe brokerlist : %v", err)
		}
	}
	//Note that SSL encryption, technically speaking, already enables 1-way authentication in which the client authenticates
	// the server certificate. So when referring to SSL authentication, it is really referring to 2-way authentication in
	// which the broker also authenticates the client certificate.
	//BootstrapBrokerString = PLAINTEXT (9092 port). BootstrapBrokerStringTls = TLS (9094 port)
	log.Printf("Bootstrap broker connection string is : %s", aws.StringValue(out.BootstrapBrokerStringTls))
	return aws.StringValue(out.BootstrapBrokerStringTls), nil
}

func (m *MSKclient) zookeeperConString(ctx context.Context, clusterArn string) (string, error) {

	param := msk.DescribeClusterInput{ClusterArn: aws.String(clusterArn)}

	out, err := m.Client.DescribeClusterWithContext(ctx, &param)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				return "", fmt.Errorf("unable to describe cluster: %s-%v", aerr.Code(), aerr.Message())
			}
		} else {
			return "", fmt.Errorf("unable to describe cluster : %v", err)
		}
	}

	log.Printf("Zookeeper connection string is : %s", aws.StringValue(out.ClusterInfo.ZookeeperConnectString))
	return aws.StringValue(out.ClusterInfo.ZookeeperConnectString), nil
}

//loops through the list of available broker nodes and topic list to create topic. One of them is a controller and topic can be created over
//controller's broker connection only. Use kafka returned error codes to check whether the broker is a controller or not.
func (k *Kafka) createTopic(ctx context.Context) error {

	brokerConArray := strings.Split(k.BrokerConn, ",")

	for _, brokerCon := range brokerConArray {

		if k.BrokerController != "" && brokerCon != k.BrokerController { //let's move to next iterator if controller has been found
			continue
		} else { //let's find who of the brokers is a controller and create topic
			for _, topicConfig := range k.Topics {
				if k.BrokerController != "" && brokerCon == k.BrokerController {
					err := k.createKafkaTopic(ctx, k.BrokerController, topicConfig) //use controller that has been found
					if err != nil {
						return err
					}
				} else {
					err := k.createKafkaTopic(ctx, brokerCon, topicConfig) //keep on finding one
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (k *Kafka) createKafkaTopic(ctx context.Context, brokerConn string, conf kafkaTopicConfig) (err error) {
	//Set Logging by uncommenting the following during testing
	//sarama.Logger = log.New(os.Stdout, "[zaplabs-msk]", log.LstdFlags)

	//Set broker config
	broker := sarama.NewBroker(brokerConn)

	// Additional configurations. Check sarama doc for more info
	config := sarama.NewConfig()
	config.Net.TLS.Enable = true

	//choosing the latest version.
	config.Version = sarama.MaxVersion

	// Open broker connection with configs defined above
	err = broker.Open(config)
	if err != nil {
		return fmt.Errorf("error occurred while opening broker connection : %v", err)
	}

	// check if the connection was OK
	_, err = broker.Connected()
	if err != nil {
		return fmt.Errorf("error occurred while connecting to the broker : %v", err)
	}

	// Setup the Topic details in CreateTopicRequest struct
	if conf.Name == "" {
		return fmt.Errorf("An empty string was provided for topic name. Exiting.")
	}
	topic := conf.Name
	topicDetail := &sarama.TopicDetail{}
	topicDetail.NumPartitions = int32(conf.NumOfPartitions)
	topicDetail.ReplicationFactor = int16(conf.ReplicationFactor)
	topicDetail.ConfigEntries = make(map[string]*string)

	topicDetails := make(map[string]*sarama.TopicDetail)
	topicDetails[topic] = topicDetail

	request := sarama.CreateTopicsRequest{
		Timeout:      time.Second * 15,
		TopicDetails: topicDetails,
	}

	// Send request to Broker
	response, err := broker.CreateTopics(&request)
	// handle errors if any
	if err != nil {
		return fmt.Errorf("error occurred while creating the topic %s: %v", conf.Name, err)
	}

	//some specific errors are captured as TopicErrors object even though CreateTopics operation didn't result in an error.
	t := response.TopicErrors
	for _, val := range t {
		if val.Err == NOT_CONTROLLER {
			return nil
		} else if val.Err == TOPIC_ALREADY_EXISTS {
			log.Printf("Topic %s already exits.\n", conf.Name)
			k.BrokerController = brokerConn
			return nil
		} else if val.Err == SUCCESS {
			log.Printf("Topic %s created successfully.\n", conf.Name)
			k.BrokerController = brokerConn
			return nil
		} else {
			return fmt.Errorf("Error occurred while creating the topic. Error code : %v. Check kafka error codes at https://kafka.apache.org/protocol#protocol_error_codes", val.Err)
		}
	}

	// close connection to broker
	defer broker.Close()
	return nil
}

func createTopics(ctx context.Context, event cfn.Event) (physicalResourceId string, data map[string]interface{}, err error) {

	log.Println("Initializing...")
	log.Println("Lambda function should be in private subnet with NAT translation to access AWS private resources or else the lambda will fail.")
	sess := session.Must(session.NewSession()) //aws session
	r53api := R53client{Client: r53.New(sess)} //r53 client
	mskapi := MSKclient{Client: msk.New(sess)} //msk client
	var kafkaApi Kafka                         //apache kafka client
	var listOfTopics []kafkaTopicConfig

	clusterArn := event.ResourceProperties["ClusterArn"].(string)
	zoneId := event.ResourceProperties["HostedZone"].(string)

	switch event.RequestType {

	case cfn.RequestCreate:

		log.Println("CREATE: starting the create operation for topics")
		log.Printf("event data :%+v\n", event)
		log.Printf("MSKClusterArn: %s. Route53HostedZone : %s", clusterArn, zoneId)

		zoneName, err := r53api.recordSet(ctx, zoneId)
		if err != nil {
			return "", nil, err
		}

		brokers, err := mskapi.brokerConString(ctx, clusterArn)
		if err != nil {
			return "", nil, err
		}

		var brokerListSsl string
		brokerList := strings.Split(brokers, ",")
		for i, broker := range brokerList {
			if i == len(brokerList)-1 {
				brokerListSsl = brokerListSsl + "SSL://" + broker
			} else {
				brokerListSsl = "SSL://" + broker + "," + brokerListSsl
			}
		}

		zookeepers, err := mskapi.zookeeperConString(ctx, clusterArn)
		if err != nil {
			return "", nil, err
		}

		var zookeeperList []string
		zkList := strings.Split(strings.ReplaceAll(zookeepers, ":2181", ""), ",")
		for _, zk := range zkList {
			ips, _ := net.LookupIP(zk)
			for _, ip := range ips {
				zookeeperList = append(zookeeperList, ip.String())
			}
		}
		zk := strings.Join(zookeeperList, ",")

		if topics, ok := event.ResourceProperties["TopicList"].([]interface{}); ok {
			for _, topic := range topics {
				var thisTopic kafkaTopicConfig
				thisTopic.Name = topic.(map[string]interface{})["Name"].(string)
				replicationFactorStr := topic.(map[string]interface{})["ReplicationFactor"].(string)
				thisTopic.ReplicationFactor, _ = strconv.Atoi(replicationFactorStr)
				numOfPartitionsStr := topic.(map[string]interface{})["NumOfPartitions"].(string)
				thisTopic.NumOfPartitions, _ = strconv.Atoi(numOfPartitionsStr)
				listOfTopics = append(listOfTopics, thisTopic)
			}
		}

		log.Printf("list of topics : %+v", listOfTopics)
		kafkaApi.Topics = listOfTopics
		kafkaApi.BrokerConn = brokers

		err = kafkaApi.createTopic(ctx) //create topics here
		if err != nil {
			return "", nil, err
		}

		data = map[string]interface{}{
			"Brokers":    brokerListSsl,
			"Zookeepers": zk, //  resolves a list of names such as z-3.kafka-stg.e30w3f.c3.kafka.us-west-2.amazonaws.com:2181 to a list of IPs without ports
			"ZoneName":   zoneName,
		}

		log.Printf("data to be returned is :%v\n", data)
		return "", data, nil

	case cfn.RequestUpdate:

		log.Println("UPDATE: starting the update operation on topics now.")
		log.Printf("event data : %+v\n", event)

		//update operation doesn't cater to changing the replication factor or num of partitions, since sarama doesn't support it.
		event.RequestType = cfn.RequestCreate
		return createTopics(ctx, event) //calling create operation again. 'sarama' create operations are idempotent it seems.

	case cfn.RequestDelete:

		log.Printf("event data :%+v\n", event)

		//Delete operation of the stack would delete the cluster itself including all the topics in it. Hence this operation
		//doesn't make sense.
		log.Println("DELETE: skipping deleting the topics. Delete a cfn stack would delete the msk cluster anyway including all topics in it")

		return
	}

	return
}

//this lambda function accepts inputs under ResourceProperties as shown below.
//{
// "ClusterArn":"arn:aws:kafka:us-west-2:508718283261:configuration/krunal1/25815693-f755-47f3-873b-aaeb92dc25d8-3",
// "HostedZone":"ADf2er223324"
// "TopicList":[
//				{
//					"Name":"MySampleTopic"
//					"ReplicationFactor":"3"
//					"NumOfPartitions":"1"
//				},
//				...
//				]
//}

//The lambda returns the following response (sample output)
// {
//		"Brokers":"SSL://b-2.mm.rora1l.c3.kafka.us-west-2.amazonaws.com:9094,SSL://b-3.mm.rora1l.c3.kafka.us-west-2.amazonaws.com:9094,SSL://b-1.mm.rora1l.c3.kafka.us-west-2.amazonaws.com:9094",
//      "Zookeepers":"10.133.33.244,10.133.34.68,10.133.32.160",
//		"ZoneName": "mydomain.example.com"
//}
func main() {
	lambda.Start(cfn.LambdaWrap(createTopics))
}
