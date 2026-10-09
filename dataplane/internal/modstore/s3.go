package modstore

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MaxModuleBytes caps how much S3.Get reads; larger objects are an error.
const MaxModuleBytes = 64 << 20

// S3Options configures an S3 store.
type S3Options struct {
	Endpoint  string // host:port, without scheme
	Bucket    string
	Region    string // set explicitly so the client never asks the server for the bucket location
	AccessKey string
	SecretKey string
	Secure    bool
}

// S3 is a Store backed by "<hex>.wasm" objects in an S3 bucket.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 creates an S3 store. It makes no request.
func NewS3(opts S3Options) (*S3, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("modstore: s3 endpoint is empty")
	}
	if opts.Bucket == "" {
		return nil, errors.New("modstore: s3 bucket is empty")
	}
	client, err := minio.New(opts.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(opts.AccessKey, opts.SecretKey, ""),
		Secure: opts.Secure,
		Region: opts.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("modstore: s3 client: %w", err)
	}
	return &S3{client: client, bucket: opts.Bucket}, nil
}

// Get downloads the module for hash and verifies its content matches.
// Errors other than ErrBadHash, ErrNotFound and ErrHashMismatch are transient
// storage failures and wrap the client error.
func (s *S3) Get(ctx context.Context, hash string) ([]byte, error) {
	hexDigest, err := ParseHash(hash)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, hexDigest+".wasm", minio.GetObjectOptions{})
	if err != nil {
		return nil, s.mapErr(hash, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, MaxModuleBytes+1))
	if err != nil {
		return nil, s.mapErr(hash, err)
	}
	if len(data) > MaxModuleBytes {
		return nil, fmt.Errorf("modstore: module %s exceeds %d bytes", hash, MaxModuleBytes)
	}
	if HashOf(data) != hash {
		return nil, fmt.Errorf("%w: %s", ErrHashMismatch, hash)
	}
	return data, nil
}

func (s *S3) mapErr(hash string, err error) error {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	return fmt.Errorf("modstore: s3 get %s: %w", hash, err)
}
