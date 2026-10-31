package storage

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 stores images in a bucket; credentials come from the default AWS chain (task role on ECS).
type S3 struct {
	client  *s3.Client
	bucket  string
	baseURL string
}

// NewS3 builds the store. endpoint is optional (an S3-compatible server, path-style); baseURL is the public
// prefix (CloudFront or bucket URL) that the returned URLs start with.
func NewS3(ctx context.Context, bucket, endpoint, baseURL string) (*S3, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &S3{client: client, bucket: bucket, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

// Put uploads the object (private bucket; public access goes through baseURL).
func (s *S3) Put(ctx context.Context, key, contentType string, body []byte) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(body))),
		CacheControl:  aws.String("public, max-age=31536000, immutable"),
	})
	if err != nil {
		return "", fmt.Errorf("storage: s3 put: %w", err)
	}
	return s.baseURL + "/" + key, nil
}
