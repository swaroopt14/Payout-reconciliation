package storage

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Store struct {
	Client               *s3.Client
	CanonicalBucketName  string
	NIRBucketName        string
	GovernanceBucketName string
}

func NewS3Store(ctx context.Context, canonicalBucketName, nirBucketName, governanceBucketName string, region string) (*S3Store, error) {
	if canonicalBucketName == "" {
		return nil, fmt.Errorf("canonical S3 bucket name is required")
	}
	if nirBucketName == "" {
		return nil, fmt.Errorf("NIR S3 bucket name is required")
	}
	if governanceBucketName == "" {
		return nil, fmt.Errorf("governance S3 bucket name is required")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg, withPathStyleFromEnv)

	return &S3Store{
		Client:               client,
		CanonicalBucketName:  canonicalBucketName,
		NIRBucketName:        nirBucketName,
		GovernanceBucketName: governanceBucketName,
	}, nil
}
