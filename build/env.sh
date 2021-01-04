#!/bin/bash

ROOT="$(cd $(dirname $0)/.. && pwd)" #repository root path

# make sure gitlab project has AWS credentials for each account named as shown below for
# example dev account credentials would start with 'DEV_'
if [ ${GIT_BRANCH} == "master" ]; then
    STAGE=prd
    export AWS_ACCESS_KEY_ID=${PRD_AWS_ACCESS_KEY_ID}
    export AWS_SECRET_ACCESS_KEY=${PRD_AWS_SECRET_ACCESS_KEY}
    export AWS_REGION=${PRD_AWS_REGION}
    export AWS_ACCOUNT=${PRD_AWS_ACCOUNT_ID}
    export AWS_SHARED_ACCOUNT=${PRD_AWS_SHARED_ACCOUNT_ID}
elif [ ${GIT_BRANCH} == "stage" ]; then
    STAGE=stg
    export AWS_ACCESS_KEY_ID=${STG_AWS_ACCESS_KEY_ID}
    export AWS_SECRET_ACCESS_KEY=${STG_AWS_SECRET_ACCESS_KEY}
    export AWS_REGION=${STG_AWS_REGION}
    export AWS_ACCOUNT=${STG_AWS_ACCOUNT_ID}
    export AWS_SHARED_ACCOUNT=${STG_AWS_SHARED_ACCOUNT_ID}
elif [ ${GIT_BRANCH} == "development" -a ${GIT_BRANCH} == "devops"  ];then
    STAGE=qa
    export AWS_ACCESS_KEY_ID=${QA_AWS_ACCESS_KEY_ID}
    export AWS_SECRET_ACCESS_KEY=${QA_AWS_SECRET_ACCESS_KEY}
    export AWS_REGION=${QA_AWS_REGION}
    export AWS_ACCOUNT=${QA_AWS_ACCOUNT_ID}
    export AWS_SHARED_ACCOUNT=${QA_AWS_SHARED_ACCOUNT_ID}
else
    STAGE=dev
    export AWS_ACCESS_KEY_ID=${DEV_AWS_ACCESS_KEY_ID}
    export AWS_SECRET_ACCESS_KEY=${DEV_AWS_SECRET_ACCESS_KEY}
    export AWS_REGION=${DEV_AWS_REGION}
    export AWS_ACCOUNT=${DEV_AWS_ACCOUNT_ID}
    export AWS_SHARED_ACCOUNT=${DEV_AWS_SHARED_ACCOUNT_ID}
fi