package s3

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"

	. "github.com/cmcoffee/kitebroker/core"
)

// proxyHTTPClient returns an *http.Client routing requests through proxyURI.
// Invalid proxy URIs fall back to the default client so a misconfigured proxy
// does not silently break direct connectivity.
func proxyHTTPClient(proxyURI string) *http.Client {
	u, err := url.Parse(proxyURI)
	if err != nil {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
}

// S3Config holds the connection settings for an S3 (or S3-compatible) source.
type S3Config struct {
	AccessKey string
	SecretKey string
	Region    string
	Bucket    string
	Prefix    string
	Endpoint  string // Optional: custom endpoint for MinIO/Wasabi/other S3-compatible stores.
}

// S3Source wraps an S3 client bound to a single bucket.
type S3Source struct {
	client *awss3.S3
	bucket string
}

// S3Object represents a single object discovered in the bucket.
type S3Object struct {
	Key      string // Full object key, e.g. "reports/2026/q1.pdf".
	Size     int64
	Modified time.Time
}

// NewS3Source constructs an S3 client from the provided config. When Endpoint
// is set, path-style addressing is enabled so S3-compatible stores work.
func NewS3Source(cfg S3Config, proxyURI string) (*S3Source, error) {
	if IsBlank(cfg.Bucket) {
		return nil, fmt.Errorf("no S3 bucket configured")
	}

	aws_cfg := &aws.Config{
		Region:      aws.String(cfg.Region),
		Credentials: credentials.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey, NONE),
	}

	if !IsBlank(cfg.Endpoint) {
		aws_cfg.Endpoint = aws.String(cfg.Endpoint)
		aws_cfg.S3ForcePathStyle = aws.Bool(true)
	}

	if !IsBlank(proxyURI) {
		aws_cfg.HTTPClient = proxyHTTPClient(proxyURI)
	}

	sess, err := session.NewSession(aws_cfg)
	if err != nil {
		return nil, fmt.Errorf("Error creating S3 session: %v", err)
	}

	return &S3Source{
		client: awss3.New(sess),
		bucket: cfg.Bucket,
	}, nil
}

// ListObjects enumerates all objects under prefix, invoking fn for each. It
// pages through results so buckets with millions of objects stream rather than
// buffer. Zero-byte "folder marker" keys (keys ending in "/") are skipped.
func (S *S3Source) ListObjects(prefix string, fn func(S3Object) error) error {
	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(S.bucket),
	}
	if !IsBlank(prefix) {
		input.Prefix = aws.String(prefix)
	}

	var walk_err error
	err := S.client.ListObjectsV2Pages(input, func(page *awss3.ListObjectsV2Output, last bool) bool {
		for _, o := range page.Contents {
			key := aws.StringValue(o.Key)
			// Skip folder-marker keys (0-byte keys ending in "/").
			if key == NONE || key[len(key)-1] == '/' {
				continue
			}
			obj := S3Object{
				Key:      key,
				Size:     aws.Int64Value(o.Size),
				Modified: aws.TimeValue(o.LastModified),
			}
			if err := fn(obj); err != nil {
				walk_err = err
				return false
			}
		}
		return true
	})
	if err != nil {
		return fmt.Errorf("Error listing objects in bucket '%s': %v", S.bucket, err)
	}
	return walk_err
}

// Download returns a seekable reader over the object body. Kiteworks' chunked,
// resumable Upload seeks the source (to byte 0 at start, and to chunk offsets on
// resume), but S3 GetObject bodies are forward-only streams; s3Reader satisfies
// ReadSeekCloser by re-issuing ranged GetObject requests on Seek.
func (S *S3Source) Download(key string, size int64) ReadSeekCloser {
	return &s3Reader{
		client: S.client,
		bucket: S.bucket,
		key:    key,
		size:   size,
	}
}

// s3Reader is a seekable, lazily-opened reader over a single S3 object. It opens
// the HTTP body on first Read (or after a Seek) using a Range header so that
// arbitrary offsets are supported without buffering the whole object.
type s3Reader struct {
	client *awss3.S3
	bucket string
	key    string
	size   int64
	offset int64
	body   io.ReadCloser
}

// open lazily starts a ranged GetObject at the current offset.
func (r *s3Reader) open() error {
	input := &awss3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(r.key),
	}
	if r.offset > 0 {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-", r.offset))
	}
	out, err := r.client.GetObject(input)
	if err != nil {
		return fmt.Errorf("Error downloading '%s': %v", r.key, err)
	}
	r.body = out.Body
	return nil
}

// Read implements io.Reader, opening the body on demand.
func (r *s3Reader) Read(p []byte) (int, error) {
	if r.body == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.body.Read(p)
	r.offset += int64(n)
	return n, err
}

// Seek repositions the reader. Any open body is closed so the next Read
// re-opens at the new offset via a Range request.
func (r *s3Reader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.offset + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, fmt.Errorf("s3Reader.Seek: invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("s3Reader.Seek: negative position %d", abs)
	}
	if abs != r.offset {
		if r.body != nil {
			r.body.Close()
			r.body = nil
		}
		r.offset = abs
	}
	return r.offset, nil
}

// Close releases the underlying HTTP body, if open.
func (r *s3Reader) Close() error {
	if r.body != nil {
		err := r.body.Close()
		r.body = nil
		return err
	}
	return nil
}
