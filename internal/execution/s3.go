package execution

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// maxObject bounds one object read back, so a hostile or broken worker cannot exhaust the
// coordinator's memory with one output.
const maxObject = 512 << 20

// S3Store is an object store in one bucket under one prefix: S3 on EKS, MinIO beside kind.
type S3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

// S3Env is every variable S3FromEnv reads, which a worker needs as its coordinator has them.
var S3Env = []string{"ABOM_S3_ENDPOINT", "ABOM_S3_REGION", "ABOM_S3_BUCKET", "ABOM_S3_PREFIX", "ABOM_S3_PATH_STYLE"}

// S3FromEnv configures the store from ABOM_S3_* and the SDK's own credential chain, which reads
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or Pod Identity on EKS.
func S3FromEnv(ctx context.Context) (*S3Store, error) {
	bucket := os.Getenv("ABOM_S3_BUCKET")
	if bucket == "" {
		return nil, fmt.Errorf("ABOM_S3_BUCKET names no bucket for attempt artifacts")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cmp.Or(os.Getenv("ABOM_S3_REGION"), "us-east-1")))
	if err != nil {
		return nil, fmt.Errorf("configuring S3: %w", err)
	}
	endpoint := os.Getenv("ABOM_S3_ENDPOINT")
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = os.Getenv("ABOM_S3_PATH_STYLE") == "true"
	})
	prefix := strings.TrimSuffix(os.Getenv("ABOM_S3_PREFIX"), "/")
	if prefix != "" {
		prefix += "/"
	}
	return &S3Store{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *S3Store) Put(ctx context.Context, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key),
		Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body)))})
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, maxObject+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxObject {
		return nil, fmt.Errorf("%s is larger than %d bytes", key, maxObject)
	}
	return body, nil
}
