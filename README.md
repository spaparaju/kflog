# kflog
This tool scans though AWS VPC flow logs of the VPC where a OpenShift cluster is installed and alerts for any network failures between any of the OpenShift cluster related AWS resources.

## usage
1. Set up AWS VPC callflow. Currently the logs are written to the S3 bucket that gets created for managed OpenShift clusters like ROSA.
aws ec2 create-flow-logs \
  --resource-ids VPC_ID \
  --resource-type VPC \
  --traffic-type ALL \
  --log-destination-type s3 \
  --log-destination arn:aws:s3:::S#_BUCKET_NAME \
  --region us-east-1 \
  --log-format '${version} ${vpc-id} ${subnet-id} ${instance-id} ${interface-id} ${account-id} ${type} ${srcaddr} ${dstaddr} ${srcport} ${dstport} ${pkt-srcaddr} ${pkt-dstaddr} ${protocol} ${bytes} ${packets} ${start} ${end} ${action} ${tcp-flags} ${log-status}'

2.  kflog AWS_REGION YOUR_OPENSHIFT_CLUSTER_NAME

## next steps

 Currently this tool captures the VPCflow logs. The next step is to add more semantic information for the IP addresses listed in these logs and start alert on any network traffic flows.