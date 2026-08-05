// Package s3 is the S3 / R2 blob adapter. Atomic writes via If-Match on
// ETag (or If-None-Match:* for absence). Works with any S3-compatible
// service - pass an EndpointResolverV2 for MinIO / R2 / Wasabi.
//
// Auth: reuses the AWS SDK default credential chain. For federated creds
// (Phase 4 auth providers), the caller pre-populates env vars or an
// aws.Config before constructing New.
package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/FynxLabs/reeve/internal/blob"
)

// Store implements blob.Store against an S3 bucket.
type Store struct {
	client *s3.Client
	bucket string
	prefix string // optional key prefix (includes trailing slash if non-empty)
}

// Options configures New.
type Options struct {
	Bucket       string
	Region       string
	Prefix       string
	Endpoint     string // custom endpoint (MinIO, R2, etc.)
	UsePathStyle bool   // true for MinIO
	AccessKey    string // optional explicit creds; otherwise use default chain
	SecretKey    string
	SessionToken string
}

// New returns a Store. Uses default AWS credential chain unless explicit
// keys are supplied.
func New(ctx context.Context, opts Options) (*Store, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if opts.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(opts.Region))
	}
	if opts.AccessKey != "" && opts.SecretKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     opts.AccessKey,
					SecretAccessKey: opts.SecretKey,
					SessionToken:    opts.SessionToken,
					Source:          "reeve",
				}, nil
			}),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	s3opts := []func(*s3.Options){}
	if opts.Endpoint != "" {
		s3opts = append(s3opts, func(o *s3.Options) { o.BaseEndpoint = aws.String(opts.Endpoint) })
	}
	if opts.UsePathStyle {
		s3opts = append(s3opts, func(o *s3.Options) { o.UsePathStyle = true })
	}
	prefix := opts.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Store{
		client: s3.NewFromConfig(cfg, s3opts...),
		bucket: opts.Bucket,
		prefix: prefix,
	}, nil
}

func (s *Store) fullKey(k string) string { return s.prefix + k }

// Get reads an object. ErrNotFound on 404.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil, blob.ErrNotFound
		}
		return nil, nil, err
	}
	md := &blob.Metadata{
		ETag: strings.Trim(aws.ToString(out.ETag), `"`),
	}
	if out.LastModified != nil {
		md.LastModified = out.LastModified.Unix()
	}
	if out.ContentLength != nil {
		md.Size = *out.ContentLength
	}
	return out.Body, md, nil
}

// Put writes unconditionally.
func (s *Store) Put(ctx context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
		Body:   bytes.NewReader(buf),
	})
	if err != nil {
		return nil, err
	}
	return &blob.Metadata{
		ETag: strings.Trim(aws.ToString(out.ETag), `"`),
		Size: int64(len(buf)),
	}, nil
}

// PutIfMatch writes only if the current ETag matches. Empty ifMatch
// means "only if absent" (If-None-Match: *).
func (s *Store) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
		Body:   bytes.NewReader(buf),
	}
	if ifMatch == "" {
		input.IfNoneMatch = aws.String("*")
	} else {
		input.IfMatch = aws.String(`"` + ifMatch + `"`)
	}
	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		if isPreconditionFailed(err) {
			return nil, blob.ErrPreconditionFailed
		}
		return nil, err
	}
	return &blob.Metadata{
		ETag: strings.Trim(aws.ToString(out.ETag), `"`),
		Size: int64(len(buf)),
	}, nil
}

// Delete removes an object. Missing is silent.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.fullKey(key)),
	})
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// List returns every key under the prefix (recursive - no delimiter).
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	var continuationToken *string
	fullPrefix := s.fullKey(prefix)
	for {
		res, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(fullPrefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range res.Contents {
			k := aws.ToString(obj.Key)
			k = strings.TrimPrefix(k, s.prefix)
			out = append(out, k)
		}
		if res.IsTruncated == nil || !*res.IsTruncated {
			break
		}
		continuationToken = res.NextContinuationToken
	}
	return out, nil
}

func isNotFound(err error) bool {
	var nf *s3types.NoSuchKey
	if errors.As(err, &nf) {
		return true
	}
	var nsb *s3types.NoSuchBucket
	if errors.As(err, &nsb) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		code := ae.ErrorCode()
		if code == "NoSuchKey" || code == "NotFound" {
			return true
		}
	}
	return false
}

// isPreconditionFailed classifies a conditional-write failure so the
// caller's mutate loop re-reads and retries instead of aborting. S3
// returns 412 PreconditionFailed when an If-Match/If-None-Match
// precondition misses, and HTTP 409 ConditionalRequestConflict when a
// conditional write loses a race against a concurrent operation on the
// same key; both mean "lost the CAS race".
func isPreconditionFailed(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return true
		}
	}
	// Some S3-compatible services return a bare 412 without a parseable
	// error code; classify by HTTP status.
	var re *awshttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusPreconditionFailed {
		return true
	}
	// Last-resort message match for services that surface the failure only
	// in the message body.
	msg := err.Error()
	return strings.Contains(msg, "PreconditionFailed") || strings.Contains(msg, "ConditionalRequestConflict")
}

// compile-time check
var _ blob.Store = (*Store)(nil)
