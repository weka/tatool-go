package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SSMInstance represents an SSM-managed EC2 instance.
type SSMInstance struct {
	InstanceID   string
	IPAddress    string
	ComputerName string
}

// Label returns a human-readable display string for the instance.
func (i SSMInstance) Label() string {
	if i.IPAddress != "" && i.ComputerName != "" {
		return fmt.Sprintf("%s  (%s / %s)", i.InstanceID, i.IPAddress, i.ComputerName)
	}
	if i.IPAddress != "" {
		return fmt.Sprintf("%s  (%s)", i.InstanceID, i.IPAddress)
	}
	return i.InstanceID
}

// newSSMClient builds an SSM client from the given region and profile.
// If region is empty the SDK resolves it from environment / instance metadata.
// If profile is empty the SDK uses the default credential chain (instance profile on EC2).
func newSSMClient(ctx context.Context, region, profile string) (*ssm.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithEC2IMDSRegion(),
	}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return ssm.NewFromConfig(cfg), nil
}

// DiscoverSSMInstances lists all SSM-managed instances visible with the given
// credentials, paging through results automatically.
func DiscoverSSMInstances(ctx context.Context, region, profile string) ([]SSMInstance, error) {
	client, err := newSSMClient(ctx, region, profile)
	if err != nil {
		return nil, err
	}
	return listInstances(ctx, client, nil)
}

// MatchInstancesByIP resolves a list of IP addresses to SSM instance IDs by
// cross-referencing with DescribeInstanceInformation. This is the preferred
// discovery path when running from a Weka backend node: use weka CLI to get
// peer IPs, then map them to instance IDs without needing ec2:DescribeInstances.
func MatchInstancesByIP(ctx context.Context, region string, ips []string) ([]SSMInstance, error) {
	client, err := newSSMClient(ctx, region, "")
	if err != nil {
		return nil, err
	}

	all, err := listInstances(ctx, client, nil)
	if err != nil {
		return nil, err
	}

	ipSet := make(map[string]bool, len(ips))
	for _, ip := range ips {
		ipSet[strings.TrimSpace(ip)] = true
	}

	var matched []SSMInstance
	for _, inst := range all {
		if ipSet[inst.IPAddress] {
			matched = append(matched, inst)
		}
	}
	return matched, nil
}

func listInstances(ctx context.Context, client *ssm.Client, filters []types.InstanceInformationStringFilter) ([]SSMInstance, error) {
	var instances []SSMInstance
	var nextToken *string

	for {
		input := &ssm.DescribeInstanceInformationInput{
			NextToken: nextToken,
		}
		if len(filters) > 0 {
			input.Filters = filters
		}

		out, err := client.DescribeInstanceInformation(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("SSM DescribeInstanceInformation: %w", err)
		}

		for _, info := range out.InstanceInformationList {
			instances = append(instances, SSMInstance{
				InstanceID:   aws.ToString(info.InstanceId),
				IPAddress:    aws.ToString(info.IPAddress),
				ComputerName: aws.ToString(info.ComputerName),
			})
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return instances, nil
}
