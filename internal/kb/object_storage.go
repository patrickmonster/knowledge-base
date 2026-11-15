package kb

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectStorage is backed by a private Railway bucket; credentials never leave the server.
type ObjectStorage interface {
	Check(context.Context) error
	Put(context.Context, string, string, io.ReadSeeker, int64) error
	DownloadURL(context.Context, string, string) (string, error)
	// Get streams an object through the app, used to show issue images inline
	// under the page's same-origin image policy.
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// Check verifies bucket access without listing, creating or deleting any objects.
func (b *railwayBucket) Check(ctx context.Context) error {
	_, err := b.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(b.bucket)})
	return err
}

type BucketConfig struct {
	Endpoint, Bucket, Region, AccessKeyID, SecretAccessKey string
	ForcePathStyle                                         bool
}
type railwayBucket struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func NewRailwayBucket(c BucketConfig) (ObjectStorage, error) {
	if c.Endpoint == "" && c.Bucket == "" && c.Region == "" && c.AccessKeyID == "" && c.SecretAccessKey == "" {
		return nil, nil
	}
	if c.Endpoint == "" || c.Bucket == "" || c.Region == "" || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("all BUCKET connection variables must be supplied")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("BUCKET_ENDPOINT must be an HTTPS origin")
	}
	if strings.ContainsAny(c.Bucket, "/\\\r\n ") {
		return nil, errors.New("invalid BUCKET_NAME")
	}
	client := s3.New(s3.Options{Region: c.Region, BaseEndpoint: aws.String(strings.TrimSuffix(c.Endpoint, "/")), UsePathStyle: c.ForcePathStyle,
		Credentials:                credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
		HTTPClient:                 &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &railwayBucket{client: client, presign: s3.NewPresignClient(client), bucket: c.Bucket}, nil
}
func (b *railwayBucket) Put(ctx context.Context, key, filename string, body io.ReadSeeker, size int64) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream"), ContentDisposition: aws.String(mime.FormatMediaType("attachment", map[string]string{"filename": filename}))})
	return err
}
func (b *railwayBucket) DownloadURL(ctx context.Context, key, filename string) (string, error) {
	out, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), ResponseContentType: aws.String("application/octet-stream"), ResponseContentDisposition: aws.String(mime.FormatMediaType("attachment", map[string]string{"filename": filename}))}, func(o *s3.PresignOptions) { o.Expires = 5 * time.Minute })
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
func (b *railwayBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
func (b *railwayBucket) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	return err
}
