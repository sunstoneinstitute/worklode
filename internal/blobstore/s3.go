package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config configures an S3-compatible backend. Hetzner Object Storage is
// Ceph RADOS Gateway behind an S3 API and requires path-style addressing,
// which NewS3 always sets -- matching the s3ForcePathStyle the fleet already
// uses for Velero and CNPG against this endpoint.
type S3Config struct {
	Endpoint  string // e.g. https://hel1.your-objectstorage.com
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
}

type s3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 builds a Store backed by an S3-compatible endpoint.
func NewS3(cfg S3Config) (Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blobstore: endpoint and bucket are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1" // SigV4 needs a region string; the gateway ignores it.
	}
	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, ""),
	})
	return &s3Store{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, mediaType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(mediaType),
		// Metadata the gateway returns on every GET, keeping the object
		// self-describing for anyone reading the bucket directly. The
		// presign overrides in PresignGet are what actually reach a browser.
		Metadata: map[string]string{
			"x-content-type-options":  "nosniff",
			"content-security-policy": "default-src 'none'; sandbox",
		},
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

func (s *s3Store) PresignGet(ctx context.Context, key string, ttl time.Duration, opts GetOptions) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentType:        aws.String(opts.ContentType),
		ResponseContentDisposition: aws.String(opts.ContentDisposition),
		ResponseCacheControl:       aws.String(opts.CacheControl),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign %s: %w", key, err)
	}
	return req.URL, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return ErrNotFound
		}
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (s *s3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			obj := Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size)}
			if o.LastModified != nil {
				obj.LastModified = o.LastModified.UTC()
			}
			out = append(out, obj)
		}
	}
	return out, nil
}
