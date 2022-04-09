package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	s3v1 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/jszwec/csvutil"

	sessionv1 "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

type LogEntry struct { // Our example struct, you can use "-" to ignore a field
	SubnetId     string `csv:"subnet_id"`
	InstanceName string `csv:"instance_name"`
	ENIId        string `csv:"eni_id"`
	SourceIp     string `csv:"source_ip"`
	DestIp       string `csv:"dest_ip"`
	SourcePort   string `csv:"source_port"`
	DestPort     string `csv:"dest_port"`
	Action       string `csv:"action"`
	Status       string `csv:"status"`
}

var csvHeader string = "subnet_id,instance_name,eni_id,source_ip,dest_ip,source_port,dest_port,action,status"

/*
*	usage : kflog AWS_REGION YOUR_OPENSHIFT_CLUSTER_NAME
 */
func main() {

	// Exit if REGION and CLUSTER_NAME are not provided. Need validation checks.
	if len(os.Args) < 3 {
		return
	}
	/*
		sess, err := session.NewSession(&aws.Config{
			Region: aws.String(os.Args[1]),
		})

		if err != nil {
			fmt.Println(" Could not setup AWS connection : " + err.Error())
			return
		}
	*/

	cfg, err := config.LoadDefaultConfig(context.TODO())
	cfg.Region = os.Args[1]

	if err != nil {
		fmt.Println(" Could not load AWS config : " + err.Error())
		return
	}

	// Get the VPCId where the OpenShift cluster is installed.
	var vpcID = "undefined"

	ec2Client := ec2.NewFromConfig(cfg)
	vpcID, err = getVPCForCluster(ec2Client, os.Args[2])
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("Your VPCId : " + vpcID)

	// Get the S3 bucket which got created along with the OpenShift cluster.
	s3Client := s3.NewFromConfig(cfg)
	bucketName, err := getS3BucketsForCluster(s3Client, os.Args[2])
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("Your S3 bucket where the VPC flowlogs written is : " + bucketName)

	/*
		// Create a new VPC flowlog.
		arnNotation := "arn:aws:s3:::"
		arnBucketName := arnNotation + bucketName

		flowID, err := createVPCFlowLog(ec2Client, vpcID, arnBucketName)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(flowID)
	*/

	keyToDownload, err := getRecentS3Object(s3Client, bucketName)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(" The recent Key ID : " + keyToDownload)

	if strings.Contains(keyToDownload, "AWSLogs") &&
		strings.Contains(keyToDownload, ".gz") {
		// The session the S3 Downloader will use
		sess, err := sessionv1.NewSession()

		// The S3 client the S3 Downloader will use
		s3Svc := s3v1.New(sess, awsv1.NewConfig().WithRegion("us-west-2"))
		downloader := s3manager.NewDownloaderWithClient(s3Svc)

		// Write the logs to a file with name 'outfile'
		file, err := os.Create("outfile")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()

		numBytes, err := downloader.Download(file,
			&s3v1.GetObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(keyToDownload),
			})
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println(" Downloaded the VPC flowlogs : bytes # :", numBytes)
		body, err := ioutil.ReadFile(file.Name())
		if err != nil {
			log.Fatalf("unable to read file: %v", err)
		}

		var buf2 bytes.Buffer
		err = gunzipWrite(&buf2, body)
		if err != nil {
			log.Fatal(err)
		}

		withHeader := csvHeader + "\n" + buf2.String()
		completeCSV := strings.ReplaceAll(withHeader, " ", ",")
		//	fmt.Println("decompressed:\t", completeCSV)
		//	fmt.Println(completeCSV)

		var entries []LogEntry
		if err := csvutil.Unmarshal([]byte(completeCSV), &entries); err != nil {
			fmt.Println("error:", err)
			return
		}

		for _, u := range entries {
			fmt.Printf("%+v\n", u)
		}

	}
}

func getVPCForCluster(ec2Client *ec2.Client, clusterName string) (string, error) {
	var vpcID = "undefined"

	vpcs, getErr := getAllVPCs(ec2Client)
	if getErr != nil {
		fmt.Println(getErr)
		return vpcID, errors.New("COULD NOT RETRIEVE VPCS")
	}

	for _, vpc := range vpcs {

		for _, tag := range vpc.Tags {
			if strings.Contains(*tag.Key, "Name") {
				//				fmt.Printf("Found running instance: %s\n", *tag.Key)
				if strings.Contains(*tag.Value, clusterName) {
					vpcID = *vpc.VpcId
					break
				}

			}
		}
	}
	return vpcID, nil
}

// Get all of the VPCs configured in the environment
func getAllVPCs(ec2client *ec2.Client) ([]types.Vpc, error) {
	result, err := ec2client.DescribeVpcs(context.Background(), &ec2.DescribeVpcsInput{})

	//If we had an error, return it
	if err != nil {
		return nil, err
	}

	//Otherwise, return all of our VPCs
	return result.Vpcs, nil
}

func getS3BucketsForCluster(s3Client *s3.Client, clusterName string) (string, error) {
	var bucketName = "undefined"

	s3Buckets, getErr := getAllS3Buckets(s3Client)
	if getErr != nil {
		return bucketName, getErr
	}

	for _, bucket := range s3Buckets.Buckets {
		if strings.Contains(*bucket.Name, clusterName) {
			bucketName = *bucket.Name
			break
		}
	}
	return bucketName, nil
}

// Get all of the S3 buckets configured in the environment
func getAllS3Buckets(s3Client *s3.Client) (*s3.ListBucketsOutput, error) {
	input := s3.ListBucketsInput{}
	result, err := s3Client.ListBuckets(context.Background(), &input)
	if err != nil {
		return nil, errors.New("COULD NOT RETRIEVE S3 BUCKETS")
	}
	return result, nil
}

// Get all of the S3 buckets configured in the environment
func getRecentS3Object(s3Client *s3.Client, bucketName string) (string, error) {

	input := s3.ListObjectsInput{
		Bucket: &bucketName,
	}

	result, err := s3Client.ListObjects(context.Background(), &input)
	if err != nil {
		return "", errors.New("COULD NOT RETRIEVE OBJECTS from the S3 BUCKET : " + bucketName)
	}

	currentTime := time.Now().UnixMilli()
	var delta int64 = 100000000
	var recentKey string = "undefined"

	for _, c := range result.Contents {
		t := c.LastModified.UnixMilli()
		if currentTime-t < delta {
			recentKey = *c.Key
			delta = currentTime - t
		}
	}

	fmt.Println(recentKey)
	fmt.Println(delta)

	return recentKey, nil
}

func gunzipWrite(w io.Writer, data []byte) error {
	reader, _ := gzip.NewReader(bytes.NewBuffer(data))
	defer reader.Close()

	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}
	w.Write(data)
	return nil
}

func createVPCFlowLog(ec2Client *ec2.Client, vpcId, arnBucketName string) ([]string, error) {

	logOptions := "${subnet-id} ${instance-id} ${interface-id}  ${srcaddr} ${dstaddr} ${srcport} ${dstport} ${action} ${log-status}"
	input := ec2.CreateFlowLogsInput{
		ResourceIds:        []string{vpcId},
		ResourceType:       types.FlowLogsResourceTypeVpc,
		TrafficType:        types.TrafficTypeAll,
		LogDestination:     &arnBucketName,
		LogDestinationType: types.LogDestinationTypeS3,
		LogFormat:          &logOptions,
	}
	result, err := ec2Client.CreateFlowLogs(context.Background(), &input)
	return result.FlowLogIds, err
}
