package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

/*
*	usage : kflog AWS_REGION YOUR_OPENSHIFT_CLUSTER_NAME
 */
func main() {

	// Exit if REGION and CLUSTER_NAME are not provided. Need validation checks
	if len(os.Args) < 3 {
		return
	}
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(os.Args[1]),
	})

	if err != nil {
		fmt.Println(" Could not setup AWS connection")
		return
	}

	var vpcID = "undefined"
	ec2Client := ec2.New(sess)
	vpcID, err = getVPCForCluster(ec2Client, os.Args[2])
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("Your VPCId : " + vpcID)

	s3Client := s3.New(sess)
	bucketName, err := getS3BucketsForCluster(s3Client, os.Args[2])
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("Your S3 bucket where the VPC flowlogs written is : " + bucketName)

	keyToDownload, err := getRecentS3Object(s3Client, bucketName)
	if err != nil {
		fmt.Println(err)
		return
	}

	downloader := s3manager.NewDownloader(sess)

	// Write the logs to a file with name 'outfile'
	file, err := os.Create("outfile")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()

	numBytes, err := downloader.Download(file,
		&s3.GetObjectInput{
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
	fmt.Println("decompressed:\t", buf2.String())
}

func getVPCForCluster(ec2Client *ec2.EC2, clusterName string) (string, error) {
	var vpcID = "undefined"

	vpcs, getErr := getAllVPCs(ec2Client)
	if getErr != nil {
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
func getAllVPCs(ec2client *ec2.EC2) ([]*ec2.Vpc, error) {
	vpcs, err := ec2client.DescribeVpcs(&ec2.DescribeVpcsInput{})

	//If we had an error, return it
	if err != nil {
		return []*ec2.Vpc{}, err
	}

	//Otherwise, return all of our VPCs
	return vpcs.Vpcs, nil
}

func getS3BucketsForCluster(s3Client *s3.S3, clusterName string) (string, error) {
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
func getAllS3Buckets(s3Client *s3.S3) (*s3.ListBucketsOutput, error) {
	input := s3.ListBucketsInput{}
	result, err := s3Client.ListBuckets(&input)
	if err != nil {
		return nil, errors.New("COULD NOT RETRIEVE S3 BUCKETS")
	}
	return result, nil
}

// Get all of the S3 buckets configured in the environment
func getRecentS3Object(s3Client *s3.S3, bucketName string) (string, error) {

	input := s3.ListObjectsInput{
		Bucket: &bucketName,
	}

	result, err := s3Client.ListObjects(&input)
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
