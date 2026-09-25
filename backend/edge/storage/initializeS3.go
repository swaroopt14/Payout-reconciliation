package storage

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Store struct {
	Client     *s3.Client
	BucketName string
}

func NewS3Store(ctx context.Context, bucketname string, region string) (*S3Store, error) {

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, withPathStyleFromEnv)

	return &S3Store{Client: client, BucketName: bucketname}, nil

}
