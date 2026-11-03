package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/noeulnight/memehub/apps/api/internal/config"
)

type Store struct {
	local      *localStore
	client     *s3.Client
	signer     *s3.PresignClient
	bucket     string
	ttl        time.Duration
	publicBase string
}
type SignedURL struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}
type MediaURL struct {
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
type Upload struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

func New(c config.Config) *Store {
	if c.S3Endpoint == "" {
		return nil
	}
	cfg := aws.Config{RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, Region: c.S3Region, Credentials: credentials.NewStaticCredentialsProvider(c.S3AccessKey, c.S3SecretKey, "")}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.S3Endpoint); o.UsePathStyle = c.S3PathStyle })
	public := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(c.S3PresignEndpoint); o.UsePathStyle = c.S3PathStyle })
	base, _ := url.Parse(c.S3PresignEndpoint)
	if c.S3PathStyle {
		base.Path = strings.TrimRight(base.Path, "/") + "/" + c.S3Bucket
	} else {
		base.Host = c.S3Bucket + "." + base.Host
	}
	return &Store{publicBase: strings.TrimRight(base.String(), "/"), client: client, signer: s3.NewPresignClient(public), bucket: c.S3Bucket, ttl: c.S3TTL}
}
func (s *Store) UploadURL(ctx context.Context, key, mime string, size int64) (Upload, error) {
	if s.local != nil {
		return s.local.uploadURL(key, mime, size)
	}
	out, err := s.signer.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: aws.String(mime), ContentLength: aws.Int64(size)}, func(o *s3.PresignOptions) { o.Expires = s.ttl })
	if err != nil {
		return Upload{}, err
	}
	headers := map[string]string{"Content-Type": mime}
	for k, v := range out.SignedHeader {
		if len(v) > 0 && k != "Host" && k != "Content-Length" {
			headers[k] = v[0]
		}
	}
	return Upload{URL: out.URL, Method: "PUT", Headers: headers, ExpiresAt: time.Now().Add(s.ttl)}, nil
}
func (s *Store) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.local != nil {
		return s.local.read(key)
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
func (s *Store) Put(ctx context.Context, key, mime string, data []byte) error {
	if s.local != nil {
		return s.local.put(key, data)
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: aws.String(mime), Body: bytes.NewReader(data)})
	return err
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if s.local != nil {
		return s.local.delete(key)
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}
func (s *Store) ReadURL(ctx context.Context, key string) (MediaURL, error) {
	if !fs.ValidPath(key) || !(strings.HasPrefix(key, "originals/") || strings.HasPrefix(key, "thumbnails/") || strings.HasPrefix(key, "previews/")) {
		return MediaURL{}, fmt.Errorf("invalid public storage key")
	}
	if s.local != nil {
		signed, err := s.local.signedURL("GET", key, "", 0)
		return MediaURL{URL: signed.URL, ExpiresAt: &signed.ExpiresAt}, err
	}
	target, err := url.JoinPath(s.publicBase, key)
	if err != nil {
		return MediaURL{}, err
	}
	return MediaURL{URL: target}, nil
}

func ImageExtension(mime string) string {
	return map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}[mime]
}
