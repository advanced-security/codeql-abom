package execution

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// maxObject bounds any one object read back, so a hostile or broken worker cannot exhaust the
// coordinator's memory with one output; a read that knows what it reads is bounded tighter.
const maxObject = 512 << 20

// readBounded reads body, refusing it with ErrTooLarge past limit, and limit past maxObject.
func readBounded(key string, body io.Reader, limit int64) ([]byte, error) {
	limit = min(limit, maxObject)
	out, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("reading %s: %w: %d bytes", key, ErrTooLarge, limit)
	}
	return out, nil
}

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

func (s *S3Store) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	var missing *types.NoSuchKey
	if errors.As(err, &missing) {
		return nil, fmt.Errorf("reading %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	defer out.Body.Close()
	if n := out.ContentLength; n != nil && *n > min(limit, maxObject) {
		return nil, fmt.Errorf("reading %s: %w: %d bytes", key, ErrTooLarge, min(limit, maxObject))
	}
	return readBounded(key, out.Body, limit)
}
