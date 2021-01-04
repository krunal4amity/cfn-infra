# cfn-infra

### pre-requisites

This repository creates infrastructure.
It needs to be run as a pre-requisite for each stage
`dev|qa|stg|prd` before running respective application
build pipeline. The value of `stage` is used at
many places in the template to create aws specific and
application specific resources.

Please resolve the `todo` items before you go ahead running
the pipeline.

Make sure the following environment variables are available
in your pipeline tool.

- `<stage>`\_AWS_ACCESS_KEY_ID
- `<stage>`\_AWS_SECRET_ACCESS_KEY
- `<stage>`\_AWS_ACCOUNT_ID
- `<stage>`\_AWS_SHARED_ACCOUNT_ID
- `<stage>`\_AWS_REGION

### infra-components

This repository creates the following infrastructure.

- Elasticache of type Redis (2000-elasticache.yml)
- Elastic Container service for Kubernetes (2000-eks.yml)
- Managed Service for Kafka (2000-msk.yml)
- Postgresql database (2000-rds.yml)
- Elastic Map Reduce (2000-emr.yml)
- ElasticSearch (2000-elasticsearch.yml)
- SSM parameters (1000-ssm.yml)

Recommended values for `stagename` are `dev|qa|stg|prd` in lowercase, although
any values can be chosen, although SSM Parameters template's Mapping
section needs to be updated accordingly.

All the templates have a condition name `IsProd` which
evaluates to true if the `stagename` is `prd` (and not "prod").

#### SSM Parameters

- 1000-ssm.yml is the cloudformation template
- contains parameters that are to be used by all the stacks.
- Used in stack templates using dynamic reference e.g. {{param}}
- Contains a mapping of fairly static elements e.g. vpc, route53 domain,
  ssh key pair.
- The `stage` argument to be supplied to pipeline tool should
  be mapped to the Mapping section for each stack. e.g. if 'stage'
  is `dev`, the Mappings section should contain an inner 'dev' section for
  each stack.
- SSM has been chosen over SecretsManager since secretmanager
  cannot be dynamically referenced in Cloudformation custom resources
  as of now whereas SSM parameter can be.
- Please check and update this templates for parameters
  that are customizable for each stack.
- Keep in mind the Cloudformation limit of 200 resources
  per stack, since each parameter in the template is a resource.
- takes about a minute to deploy.

#### RDS

- 2000-rds.yml is the cloudformation template
- Creates a `private` postgresql database instance for non-prod.
  For prod, creates a slave instance as well.
- creates necessary cloudwatch alarms mapped to an sns topic,
  only to be created for prod env.
- A KMS key is created to create a Secret that will hold the
  password for the postgresql dba user `mydbuser`. The database
  created will be `mydb`.
- An ec2 instance will be created to import data into postgres
  database or run any sql scripts required. The sql scripts
  are to be kept under `resources/_postgres/<stagename>`, if
  an import-order is important then make sure the alphabetical
  order of the files is the same as required import-order.
- creates Route53 record set for both Primary instance and
  slave instance (if applicable). E.g. If the private Route53 hosted
  zone is `private.example.com` and stagename is `dev` then Primary DB instance will be
  resolvable at `db-dev.private.example.com` and Slave
  instance is resolvable at `db-slave-dev.private.example.com`.
  The format is `db-<stagename>.<Private Zone Name>`.
- WARNING: please alter DbParam Resource in the cloudformation template
  according to the parameters required for postgresql database
  based on performance requirements before production use.
- termination protection is enabled for the master db in production
  as a safety measure.
- takes about 45 minutes to deploy for production.
- Note: Production database is set to be not-terminated. Hence
  before deleting production stack for rds, manually change the
  termination-enabled flag for RDS.

#### EKS

- 2000-eks.yml is the cloudformation template
- Creates an EKS cluster, nodes and required infrastructure
  such as security groups etc.
- EKS cluster is created using a cloudformation custom resource.
  This allows selection of features that are not available through
  standard AWS::EKS::Cluster resource e.g. cloudwatch logging
  options and API Server access modes. In addition it also
  allows automate the creation of ConfigMap that allows
  nodes to communicate with the EKS cluster.
- EKS cluster resource has a property called `AccessMode` whic
  takes values from `FullPublic|HalfPublic|FullPrivate`. `FullPublic`
  creates a publicly accessible API Server. `HalfPublic` creates
  a publicly accessible API Server which can be connected from
  within vpc from vpc-scoped network components. `FullPrivate`
  creates an API Server which is not publicly available and
  can only be accessed from within vpc (or corporate network).
  A cicd tool's executors e.g. gitlab managed runners may not be
  able to access such API server.
- A service account is also created that can be
  used for deployment to the eks cluster. A kubeconfig
  file meant for the service account is created and
  uploaded to an S3 bucket for usage.
- An ec2 instance is created to run kubectl command which
  creates a ConfigMap and also any kubernetes application
  files placed under `resources/_kube/<stagename>`, Kubectl
  will apply them in alphabetical order, so if apply-order matters
  ensure that the files have the same alphabetical order as
  the required apply-order.
- This template is intended to be used for datplatform
  web-ui, microservices and nifi. If these are to be hosted
  on separate eks clusters then create that many eks template
  files and change `AppName` cloudformation parameter accordingly.
- `AWS Fargate for EKS` is generally available since
  03-Dec-19, It can be considered a viable option however
  the following points need to be considered about it - 1. There is a maximum
  of 4 vCPU and 30Gb memory per pod. 2. no support for stateful
  workloads that require persistent volumes 3. cannot run Daemonsets,
  Privileged pods, or pods that use HostNetwork or HostPort
- Recently in Dec-19, a resource `AWS::EKS::Nodegroup` was
  added to aws cloudformation. However, since we are
  creating an EKS cluster using a custom resource, it may
  not bode well with waiting for eks cluster to become
  active etc., so it was not explored.

#### Elasticache

- 2000-elasticache.yml is the cloudformation template.
- Creates a `private` Replication Group which is a collection of
  Elasticache clusters. At-rest and In-transit encryption
  are enabled. Allows automatic failover in case the primary-endpoint
  in unvailable. A separate KMS key is created for the replication
  group.
- Creates a Route53 record set to resolve to redis cluster.
  E.g. if the private route53 zone name is `private.example.com`
  and stagename is `dev` then redis cluster is resolvable at
  `dataplatform-redis-dev.private.example.com` and port `6379`

#### EMR

- 2000-emr.yml is the cloudformation template
- Creates a `private` EMR cluster MasterInstanceGroup, CoreInstanceGroup
  and TaskInstanceGroup with supporting infrastructure
  e.g. iam roles, security groups etc. Creating a `public`
  emr cluster requires different configuration in the template
- Creates a route53 record set for the master emr. E.g. if the
  private route53 zone is `private.example.com` and stagename
  is `dev` then master emr is resolvable at
  `dataplatform-masteremr-dev.private.example.com`.
- Current Issue: Master and Slave security groups
  cross reference each other, hence while deleting the stack,
  the deletion fails. Consider manually removing the security
  group ingress rules from the security groups that fail to get
  deleted.

#### ElasticSearch

- 2000-elasticsearch.yml is the cloudformation template
- Creates a VPC scoped elasticsearch domain.
- It is required by AWS that when creating a VPC domain,
  you must choose a subnet that uses either 10.x.x.x or
  172.x.x.x for its CIDR block.
- AWS::Elasticsearch::Domain does not support creating
  public log options. Hence a custom lambda resource
  has been created to create index,search,app log options
  for the domain. This log options are only created for
  production. (As of 07-Nov-19, a LogPublishingOption property
  is available for ElasticSearch domain.)
- For non-prod, it creates three instances by default,
  where as for prod the number is customizable in the SSM
  templates.
- creates cloudwatch alerts for the domains for prod.
- Note: currently there's an issue with creating an
  elasticsearch service linked role using cloudformation.
  As an alternative execute the following one-off command per aws
  account as a pre-requisite before running the template: `aws iam create-service-linked-role --aws-service-name es.amazonaws.com`
- Creates a route53 record set for elasticsearch vpc endpoint.
  E.g. if the private route53 domain name is `private.example.com`
  and stagename is `dev` then Elasticsearch domain is
  resolvable to `dataplatform-es-dev.private.example.com`

#### Managed Service for Kafka

- 2000-msk.yml is the cloudformation template
- Creates an MSK Cluster Configuration, MSK Cluster,
  Topics on the msk cluster, Schema Registry on an EC2.
- Make sure a VPC with atleast 3 private subnets in different
  availability zones has been selected for creating a cluster.
- AWS::MSK::Cluster does not support creating Cluster
  configuration hence a custom lambda function is written
  to perform that. `Note`: Please make sure that the cloudformation
  custom resource `KafkaPreProcessor` is updated with required
  cluster properties based on performance requirements.
- MSK cluster is a private cluster with TLS enabled meaning
  that clients can verify server's authenticity, however
  clients are not required to authenticate themselves. At-rest
  and In-transit encryption is enabled.
- Once the cluster is created, a custom resource creates
  some default topics on the cluster, which is useful if
  `create.topics.enable` property is not enabled, as recommeded by Kafka.
  The purpose of the function is to perform any postprocessing
  that may be required to perform but for now only supports
  creating topics through `KafkaPostProcessor` custom resource.
- SchemaRegistry is not available by default with MSK
  cluster hence an autoscaling group for ec2 instances will
  be created that will host the schema-registry to update
  or read schemas.
- Currently no api is available to delete the cluster configuration.
- Updating cluster configuration is also not possible, since
  update-cluster-configuration operation is supported on
  an existing cluster, which could create cyclic dependency
  in the template if mentioned. Hence for now, it needs to
  be done manually.
- Deleting the stack could take some 40 minutes easily
  due to vpc-scoped network interfaces held by lamda (custom
  resources) need to be deleted, which takes time apparently.
- Creates Route53 record sets to connect to Zookeepers and
  schema registries. E.g. if the private route53 zone name
  is `private.example.com` and stagename is `dev` then
  a list of zookeepers will be resolvable at
  `dataplatform-zookeepers-dev.private.example.com` and
  a list of schema registry ec2 will be resolvable at
  `dataplatform-schemareg-dev.private.example.com`
- stack creation time is around 25 minutes

#### gitlab runner

- 3000-gitlab.yml is the cloudformation template.
- It creates a private gitlab runner in the aws account being
  used according to the `stage` set in. It allows private
  access to internal resources e.g. private artifactory, nexus etc
  which is not possible using shared public runners.
- It runs just like any other gitlab runner.
- It has access to the account that it is created in according
  to the IAM role assigned to it in the template. It doesn't
  need to be compute heavy instance and can be light-weight e.g.
  t2.micro, t2.small.
- each runner will be tagged according to the stage it is being
  run in.
- It uses a `GitLabRunnerToken` parameter, which can be
  fetched from gitlab's project/group/subgroup -> Setting -> CI/CD.
  Please replace it with a valid value before using it.
- Ideally `GitLabRunnerToken` value should be a gitlab Group's
  token (a group that is specific to a business unit) so
  that it can be used by all the projects under that group.
