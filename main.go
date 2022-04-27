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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	sessionv1 "github.com/aws/aws-sdk-go/aws/session"
	s3v1 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/fatih/color"
	"github.com/jszwec/csvutil"
)

type LogEntry struct { // Our example struct, you can use "-" to ignore a field
	SubnetId       string `csv:"subnet_id"`
	InstanceName   string `csv:"instance_name"`
	ENIId          string `csv:"eni_id"`
	PacketSourceIp string `csv:"packet_source_ip"`
	SourceIp       string `csv:"source_ip"`
	SourcePort     string `csv:"source_port"`
	PacketDestIp   string `csv:"packet_dest_ip"`
	DestIp         string `csv:"dest_ip"`
	DestPort       string `csv:"dest_port"`
	Action         string `csv:"action"`
	Status         string `csv:"status"`
	TCPFlags       string `csv:"tcp_flags"`
	StartTime      string `csv:"start"`
	EndTime        string `csv:"end"`
}

var csvHeader string = "subnet_id,instance_name,eni_id,packet_source_ip,source_ip,source_port,packet_dest_ip,dest_ip,dest_port,action,status,tcp_flags,bytes,packets,start,end"

/*
*	usage : kflog AWS_REGION YOUR_OPENSHIFT_CLUSTER_NAME
 */
func main() {

	// Exit if REGION and CLUSTER_NAME are not provided. Need validation checks.
	if len(os.Args) < 4 {
		fmt.Println(" Usage: kflog aws_region vpc_id 'page size for the slow network calls'")
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
	fmt.Println("setting region to : " + os.Args[1])
	cfg.Region = os.Args[1]

	if err != nil {
		fmt.Println(" Could not load AWS config : " + err.Error())
		return
	}

	// Get the VPCId where the OpenShift cluster is installed.
	var vpcID = os.Args[2]

	ec2Client := ec2.NewFromConfig(cfg)
	/*
		var vpcID = "undefined"
		vpcID, err = getVPCForCluster(ec2Client, os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Your VPCId : " + vpcID)
	*/

	/*
		// Get the S3 bucket which got created along with the OpenShift cluster.
		s3Client := s3.NewFromConfig(cfg)
		bucketName, err := getS3BucketsForCluster(s3Client, os.Args[2])
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("Your S3 bucket where the VPC flowlogs written is : " + bucketName)
	*/

	// Create a new S3 bucket with a name: cluster-name-vpc-flow-logs
	s3Client := s3.NewFromConfig(cfg)
	//	bucketLoc := s3types.BucketLocationConstraint(os.Args[1])
	bucketName := os.Args[2] + "-vpc-flow-logs"
	//	locConf := s3types.CreateBucketConfiguration{
	//		LocationConstraint: bucketLoc,
	//	}

	input := s3.CreateBucketInput{
		Bucket: &bucketName,
		//		CreateBucketConfiguration: &locConf,
	}

	_, err = s3Client.CreateBucket(context.Background(), &input)
	if err != nil {
		if strings.ContainsAny(err.Error(), "Already") {
			// Ignore the error
			fmt.Println("S3 bucket already exists  : " + bucketName)
		} else {
			fmt.Println(" Could not create a new S3 bucket : " + bucketName)
			fmt.Println(err.Error())
			return
		}
	}
	fmt.Println("Your S3 bucket where the VPC flowlogs written is : " + bucketName)

	// Create a new VPC flowlog.
	arnNotation := "arn:aws:s3:::"
	arnBucketName := arnNotation + bucketName

	flowID, err := createVPCFlowLog(ec2Client, vpcID, arnBucketName)
	if err != nil {
		fmt.Println(err)
		return
	}

	if flowID != nil && len(flowID) > 0 {
		fmt.Println("VPC flow has  been setup : " + flowID[0])
	}

	tagMap, _ := getAllNetworkInterfaces(ec2Client, vpcID)

	for {
		keyToDownload, err := getRecentS3Object(s3Client, bucketName)
		if err != nil {
			fmt.Println(err)
			return
		}
		//		fmt.Println(" The recent Key ID : " + keyToDownload)

		if strings.Contains(keyToDownload, "AWSLogs") &&
			strings.Contains(keyToDownload, ".gz") {
			// The session the S3 Downloader will use
			sess, err := sessionv1.NewSession()

			// The S3 client the S3 Downloader will use
			s3Svc := s3v1.New(sess, awsv1.NewConfig().WithRegion(os.Args[1]))
			downloader := s3manager.NewDownloaderWithClient(s3Svc)

			// Write the logs to a file with name 'outfile'
			file, err := os.Create("outfile")
			if err != nil {
				fmt.Println(err)
				return
			}
			defer file.Close()

			_, err = downloader.Download(file,
				&s3v1.GetObjectInput{
					Bucket: aws.String(bucketName),
					Key:    aws.String(keyToDownload),
				})
			if err != nil {
				fmt.Println(err)
				return
			}

			//	fmt.Println(" Downloaded the VPC flowlogs : bytes # :", numBytes)
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

			for k, v := range tagMap {
				completeCSV = strings.ReplaceAll(completeCSV, k, v)
			}

			var entries []LogEntry
			if err := csvutil.Unmarshal([]byte(completeCSV), &entries); err != nil {
				fmt.Println("error:", err)
				return
			}

			sort.SliceStable(entries, func(i, j int) bool {
				//return len(strs[i]) < len(strs[j])
				first_entry_start_time, _ := strconv.Atoi(entries[i].StartTime)
				first_entry_end_time, _ := strconv.Atoi(entries[i].EndTime)
				second_entry_start_time, _ := strconv.Atoi(entries[j].StartTime)
				second_entry_end_time, _ := strconv.Atoi(entries[j].EndTime)

				return first_entry_end_time-first_entry_start_time > second_entry_end_time-second_entry_start_time
			})

			limit, _ := strconv.Atoi(os.Args[3])

			c1 := color.New(color.FgCyan).Add(color.Underline)
			c2 := color.New(color.FgCyan)

			c1.Println("Slow network calls observed during last 10 minutes...")
			for i, u := range entries {
				first_entry_start_time, _ := strconv.Atoi(u.StartTime)
				first_entry_end_time, _ := strconv.Atoi(u.EndTime)

				c2.Printf("%+v seconds from {%s,%s} to {%s,%s} {Action:%s, Status:%s, TCP:%s} in {%s}\n", first_entry_end_time-first_entry_start_time, u.PacketSourceIp, u.SourceIp, u.PacketDestIp, u.DestIp, u.Action, u.Status, u.TCPFlags, u.SubnetId)
				if i >= limit {
					break
				}
			}
			c1.Println("Waiting for the next aggreation interval is hit to collect VPC flow logs...")
			/*
				sort.SliceStable(entries, func(i, j int) bool {
					//return len(strs[i]) < len(strs[j])
					return strings.ContainsAny(entries[i].Action, "ACCEPT")
				})

				fmt.Println(" REJECTED network calls...")
				fmt.Println(" ------------------------")
				rejectCount := 0
				fmt.Printf("%s || %s || %s || %s || %s || %s || %s || %s || %s\n", "Subnet-ID", "Packet-Source", "Source", "Packet-Dest", "Dest", "Action", "Status", "TCP Flgs", "Duration")

				for _, u := range entries {
					if strings.Contains(u.Action, "REJECT") {
						rejectCount++
						first_entry_start_time, _ := strconv.Atoi(u.StartTime)
						first_entry_end_time, _ := strconv.Atoi(u.EndTime)

						fmt.Printf("%s || %s || %s || %s || %s || %s || %s || %s || %+v\n", u.SubnetId, u.PacketSourceIp, u.SourceIp, u.PacketDestIp, u.DestIp, u.Action, u.Status, u.TCPFlags, first_entry_end_time-first_entry_start_time)
						if rejectCount >= limit {
							break
						}
					}
				}
			*/
		}
		// Sleep for 1 mins.
		time.Sleep(time.Minute * 1)
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

	//	fmt.Println(recentKey)
	//	fmt.Println(delta)

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

	interval := int32(600) // Default. AWS API not working for lower aggregation intervals
	logOptions := "${subnet-id} ${instance-id} ${interface-id}  ${pkt-srcaddr} ${srcaddr}  ${srcport} ${pkt-dstaddr} ${dstaddr} ${dstport} ${action} ${log-status} ${tcp-flags} ${bytes} ${packets} ${start} ${end}"
	input := ec2.CreateFlowLogsInput{
		ResourceIds:            []string{vpcId},
		ResourceType:           types.FlowLogsResourceTypeVpc,
		TrafficType:            types.TrafficTypeAll,
		LogDestination:         &arnBucketName,
		LogDestinationType:     types.LogDestinationTypeS3,
		MaxAggregationInterval: &interval,
		LogFormat:              &logOptions,
	}
	result, err := ec2Client.CreateFlowLogs(context.Background(), &input)

	if err != nil {
		if strings.ContainsAny(err.Error(), "Already") {
			// Ignore
			//			fmt.Println("VPC flow log is already been created : ")
			return nil, nil
		} else {
			fmt.Println("could not create VPC flow log : " + err.Error())
			return nil, err
		}
	}
	return result.FlowLogIds, err
}

func getAllNetworkInterfaces(ec2Client *ec2.Client, vpcId string) (map[string]string, error) {

	m := make(map[string]string)

	vpcArray := make([]string, 1)
	vpcArray[0] = vpcId

	filters := []ec2types.Filter{
		ec2types.Filter{
			Name:   aws.String("vpc-id"),
			Values: vpcArray,
		},
	}

	params := &ec2.DescribeNetworkInterfacesInput{
		Filters: filters,
	}

	result, err := ec2Client.DescribeNetworkInterfaces(context.Background(), params)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	for _, u := range result.NetworkInterfaces {
		t := "unknown"
		for _, tag := range u.TagSet {
			if strings.Contains(*tag.Key, "Name") {
				t = *tag.Value
				break
			}
		}

		d := "unknown"
		d = *u.Description

		if !strings.Contains(t, "unknown") {
			m[*u.PrivateIpAddress] = t
		} else {
			m[*u.PrivateIpAddress] = d
		}
	}

	return m, nil

}
