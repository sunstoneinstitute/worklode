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
	// Credentials are required here rather than at first use: an empty static
	// provider fails per-request with ErrStaticCredentialsEmpty, so a fumbled
	// secret would boot a healthy-looking server that 502s every upload and
	// every serve. Fail the boot instead.
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("blobstore: endpoint, bucket, access key and secret key are all required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1" // SigV4 needs a region string; the gateway ignores it.
	}
	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		// Since the Jan-2025 SDK change the default is WhenSupported, which
		// puts x-amz-checksum-crc32 on every PutObject. Ceph RGW's support for
		// flexible checksums is version-dependent, and this is the most common
		// way an S3-compatible gateway rejects an otherwise valid request.
		// Send one only where the API requires it.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
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
		// No user metadata: PutObjectInput.Metadata serialises as
		// x-amz-meta-<key>, which no browser reads, so it cannot carry
		// Content-Security-Policy or X-Content-Type-Options. Content-Disposition
		// on the presigned URL is what carries the serving hardening (021 §6).
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
