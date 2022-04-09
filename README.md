# kflog
This tool scans though AWS VPC flow logs of the VPC where a OpenShift cluster is installed and alerts for any network failures between any of the OpenShift cluster related AWS resources.

## usage
- kflog $AWS_REGION $YOUR_OPENSHIFT_CLUSTER_NAME

## next steps

 Currently this tool captures the VPCflow logs. The next step is to add more semantic information for the IP addresses listed in these logs and start alert on any network traffic flows.