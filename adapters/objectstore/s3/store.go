// Package s3 provides immutable ciphertext storage through the S3 API.
package s3

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	versionEntropySize = 16
	maxSinglePutBytes  = 5 * 1024 * 1024 * 1024
	maxCleanupTimeout  = 5 * time.Minute
)

var (
	// ErrUnavailable identifies an S3 availability or configuration failure.
	ErrUnavailable = objectstore.ErrUnavailable
	// ErrIntegrity identifies ciphertext that differs from its durable exact reference.
	ErrIntegrity = objectstore.ErrIntegrity
	// ErrObjectTooLarge identifies ciphertext exceeding the configured single-PUT bound.
	ErrObjectTooLarge = objectstore.ErrObjectTooLarge
)

// Config contains non-secret S3 storage configuration. Credentials and custom
// certificate authorities use the AWS SDK's standard external configuration chain.
type Config struct {
	Bucket         string
	Prefix         string
	Region         string
	Endpoint       string
	ForcePathStyle bool
	AllowHTTP      bool
	MaxObjectBytes int64
	CleanupTimeout time.Duration
}

// Client is the narrow AWS S3 client surface used by Store.
type Client interface {
	PutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	GetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

// Store keeps each owned object version at a distinct, conditionally created S3 key.
type Store struct {
	client         Client
	bucket         string
	prefix         string
	maximum        int64
	cleanupTimeout time.Duration
	random         io.Reader
	randomMu       sync.Mutex
	unsignedHTTP   bool
}

// Open loads the AWS SDK's default external credential and certificate
// configuration, then constructs an S3-compatible ciphertext store.
func Open(ctx context.Context, config Config) (*Store, error) {
	if ctx == nil {
		return nil, ErrUnavailable
	}
	if err := Validate(config); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open s3 ciphertext store: %w", err)
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(config.Region))
	if err != nil {
		return nil, ErrUnavailable
	}
	// Idenqa authenticates its own ciphertext digest. Requiring only service-
	// mandated SDK checksums avoids silently excluding compatible S3 servers.
	awsConfig.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	awsConfig.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
		options.UsePathStyle = config.ForcePathStyle
		if config.Endpoint != "" {
			options.BaseEndpoint = aws.String(config.Endpoint)
		}
	})

	return newStore(client, config, rand.Reader)
}

// New constructs a store around an already configured client. This supports
// deployment-owned HTTP transports and deterministic conformance clients.
func New(client Client, config Config) (*Store, error) {
	return newStore(client, config, rand.Reader)
}

func newStore(client Client, config Config, random io.Reader) (*Store, error) {
	if client == nil || isNilClient(client) || random == nil {
		return nil, ErrUnavailable
	}
	if err := Validate(config); err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(config.Prefix, "/")
	return &Store{
		client: client, bucket: config.Bucket, prefix: prefix,
		maximum: config.MaxObjectBytes, cleanupTimeout: config.CleanupTimeout,
		random: random, unsignedHTTP: strings.HasPrefix(config.Endpoint, "http://"),
	}, nil
}

// Put streams one bounded ciphertext object into a new physical version. A
// non-zero Object returned with an error means cleanup could not be confirmed
// and the caller must retry exact-version deletion or reconcile it durably.
func (store *Store) Put(
	ctx context.Context,
	key objectstore.Key,
	write func(io.Writer) error,
) (objectstore.Object, error) {
	if store == nil || store.client == nil || ctx == nil || key == "" || write == nil {
		return objectstore.Object{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return objectstore.Object{}, fmt.Errorf("write s3 ciphertext: %w", err)
	}
	version, err := store.newVersion()
	if err != nil {
		return objectstore.Object{}, err
	}

	reader, writer := io.Pipe()
	produced := make(chan production, 1)
	go produceCiphertext(ctx, writer, write, store.maximum, produced)
	options := make([]func(*awss3.Options), 0, 1)
	if store.unsignedHTTP {
		// The SDK hashes non-TLS request bodies before transmission, which is
		// impossible for this intentionally non-seekable stream. Plain HTTP is
		// already an explicit deployment opt-in, so select S3's supported
		// UNSIGNED-PAYLOAD mode without changing the HTTPS default.
		options = append(options, func(options *awss3.Options) {
			options.APIOptions = append(
				options.APIOptions,
				awsv4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware,
			)
		})
	}
	_, uploadErr := store.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(store.physicalKey(key, version)),
		Body: reader, ContentType: aws.String("application/octet-stream"),
		IfNoneMatch: aws.String("*"),
	}, options...)
	if uploadErr != nil {
		_ = reader.CloseWithError(uploadErr)
	} else {
		_ = reader.Close()
	}
	result := <-produced
	object, objectErr := objectstore.NewObject(objectstore.ObjectRecord{
		Key: string(key), Version: version, Size: result.written, Checksum: result.checksum,
	})
	if objectErr != nil {
		return objectstore.Object{}, ErrUnavailable
	}
	operationErr := putError(ctx, result.err, result.writeErr, uploadErr)
	if operationErr == nil {
		return object, nil
	}
	if isPreconditionFailure(uploadErr) {
		// This version belongs to an existing writer; never delete it.
		return objectstore.Object{}, operationErr
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), store.cleanupTimeout)
	defer cancel()
	if err := store.Delete(cleanupContext, object); err != nil {
		return object, operationErr
	}
	return objectstore.Object{}, operationErr
}

// Open returns a stream that checks the durable size and SHA-256 digest at EOF.
func (store *Store) Open(ctx context.Context, object objectstore.Object) (io.ReadCloser, error) {
	if store == nil || store.client == nil || ctx == nil || object.IsZero() {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open s3 ciphertext: %w", err)
	}
	record := object.Record()
	output, err := store.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(store.physicalKey(object.Key(), record.Version)),
	})
	if err != nil || output == nil || output.Body == nil {
		return nil, mapOperationError(ctx, err)
	}
	if output.ContentLength != nil && *output.ContentLength != record.Size {
		_ = output.Body.Close()
		return nil, ErrIntegrity
	}
	return &verifyingReader{
		body: output.Body, digest: sha256.New(), ctxErr: ctx.Err,
		expected: record.Checksum, size: record.Size,
	}, nil
}

// Delete removes only the exact physical object version. S3 deletion is idempotent.
func (store *Store) Delete(ctx context.Context, object objectstore.Object) error {
	if store == nil || store.client == nil || ctx == nil || object.IsZero() {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete s3 ciphertext: %w", err)
	}
	record := object.Record()
	_, err := store.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(store.physicalKey(object.Key(), record.Version)),
	})
	if err != nil {
		return mapOperationError(ctx, err)
	}
	return nil
}

// ListInventory returns one provider page beneath an owned logical prefix.
// Malformed provider entries are ignored rather than promoted into deleteable references.
func (store *Store) ListInventory(
	ctx context.Context,
	prefix objectstore.Key,
	cursor string,
	limit int,
) (objectstore.InventoryPage, error) {
	if store == nil || store.client == nil || ctx == nil || prefix == "" ||
		!objectstore.ValidInventoryCursor(cursor) || limit < 1 || limit > objectstore.MaxInventoryPageSize {
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return objectstore.InventoryPage{}, fmt.Errorf("list s3 ciphertext inventory: %w", err)
	}
	maximum := int32(limit)
	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(store.bucket), Prefix: aws.String(store.inventoryPrefix(prefix)), MaxKeys: &maximum,
	}
	if cursor != "" {
		input.ContinuationToken = aws.String(cursor)
	}
	output, err := store.client.ListObjectsV2(ctx, input)
	if err != nil || output == nil {
		return objectstore.InventoryPage{}, mapOperationError(ctx, err)
	}
	objects := make([]objectstore.InventoryObject, 0, len(output.Contents))
	for _, item := range output.Contents {
		object, ok := store.inventoryObject(item)
		if ok {
			objects = append(objects, object)
		}
	}
	next := ""
	if aws.ToBool(output.IsTruncated) {
		next = aws.ToString(output.NextContinuationToken)
		if next == "" || !objectstore.ValidInventoryCursor(next) {
			return objectstore.InventoryPage{}, ErrUnavailable
		}
	}
	page, err := objectstore.NewInventoryPage(objects, next)
	if err != nil {
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	return page, nil
}

// DeleteInventory removes one exact provider-discovered physical object after
// the core has classified it as an old, unreferenced Idenqa object.
func (store *Store) DeleteInventory(ctx context.Context, object objectstore.InventoryObject) error {
	if store == nil || store.client == nil || ctx == nil || object.IsZero() {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete s3 ciphertext inventory object: %w", err)
	}
	record := object.Record()
	_, err := store.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(store.physicalKey(object.Key(), record.Version)),
	})
	if err != nil {
		return mapOperationError(ctx, err)
	}
	return nil
}

// Validate checks provider configuration without loading credentials or making
// a network request. Distribution configuration loaders use it to fail before
// process composition starts.
func Validate(config Config) error {
	if !validLabel(config.Bucket, 255) || !validLabel(config.Region, 100) ||
		config.MaxObjectBytes < 1 || config.MaxObjectBytes > maxSinglePutBytes ||
		config.CleanupTimeout <= 0 || config.CleanupTimeout > maxCleanupTimeout {
		return ErrUnavailable
	}
	if strings.Contains(config.Bucket, "/") {
		return ErrUnavailable
	}
	if config.Prefix != "" {
		prefix := strings.TrimSuffix(config.Prefix, "/")
		if _, err := objectstore.NewKey(prefix); err != nil {
			return ErrUnavailable
		}
	}
	if config.Endpoint == "" {
		return nil
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return ErrUnavailable
	}
	if endpoint.Scheme != "https" && (!config.AllowHTTP || endpoint.Scheme != "http") {
		return ErrUnavailable
	}
	return nil
}

func validLabel(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func isNilClient(client Client) bool {
	value := reflect.ValueOf(client)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func (store *Store) physicalKey(key objectstore.Key, version string) string {
	if store.prefix == "" {
		return path.Join(string(key), version)
	}
	return path.Join(store.prefix, string(key), version)
}

func (store *Store) inventoryPrefix(prefix objectstore.Key) string {
	if store.prefix == "" {
		return string(prefix) + "/"
	}
	return path.Join(store.prefix, string(prefix)) + "/"
}

func (store *Store) inventoryObject(item awstypes.Object) (objectstore.InventoryObject, bool) {
	physicalKey := aws.ToString(item.Key)
	base := ""
	if store.prefix != "" {
		base = store.prefix + "/"
	}
	if physicalKey == "" || !strings.HasPrefix(physicalKey, base) ||
		item.Size == nil || item.LastModified == nil {
		return objectstore.InventoryObject{}, false
	}
	logical := strings.TrimPrefix(physicalKey, base)
	key, err := objectstore.NewKey(path.Dir(logical))
	if err != nil {
		return objectstore.InventoryObject{}, false
	}
	object, err := objectstore.NewInventoryObject(objectstore.InventoryRecord{
		Key: string(key), Version: path.Base(logical), Size: aws.ToInt64(item.Size),
		ModifiedAt: aws.ToTime(item.LastModified),
	})
	return object, err == nil
}

func (store *Store) newVersion() (string, error) {
	value := make([]byte, versionEntropySize)
	store.randomMu.Lock()
	_, err := io.ReadFull(store.random, value)
	store.randomMu.Unlock()
	if err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(value), nil
}

type production struct {
	written  int64
	checksum string
	err      error
	writeErr error
}

func produceCiphertext(
	ctx context.Context,
	pipe *io.PipeWriter,
	write func(io.Writer) error,
	maximum int64,
	result chan<- production,
) {
	digest := sha256.New()
	bounded := &boundedWriter{
		ctxErr: ctx.Err, writer: io.MultiWriter(pipe, digest), maximum: maximum,
	}
	err := write(bounded)
	if err != nil {
		_ = pipe.CloseWithError(err)
	} else {
		_ = pipe.Close()
	}
	result <- production{
		written: bounded.written, checksum: digestValue(digest), err: err,
		writeErr: bounded.writeErr,
	}
}

func putError(ctx context.Context, producerErr, writeErr, uploadErr error) error {
	if isPreconditionFailure(uploadErr) {
		return mapOperationError(ctx, uploadErr)
	}
	if producerErr != nil {
		if uploadErr != nil && writeErr != nil && errors.Is(producerErr, writeErr) &&
			!errors.Is(producerErr, ErrObjectTooLarge) {
			return mapOperationError(ctx, uploadErr)
		}
		return fmt.Errorf("produce s3 ciphertext: %w", producerErr)
	}
	if uploadErr != nil {
		return mapOperationError(ctx, uploadErr)
	}
	return nil
}

func mapOperationError(ctx context.Context, operationErr error) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("access s3 ciphertext: %w", err)
		}
	}
	if operationErr == nil {
		return ErrUnavailable
	}
	return ErrUnavailable
}

func isPreconditionFailure(err error) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && apiError.ErrorCode() == "PreconditionFailed"
}

func digestValue(digest hash.Hash) string {
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

type boundedWriter struct {
	ctxErr   func() error
	writer   io.Writer
	maximum  int64
	written  int64
	writeErr error
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if err := writer.ctxErr(); err != nil {
		writer.writeErr = err
		return 0, err
	}
	if int64(len(value)) > writer.maximum-writer.written {
		writer.writeErr = ErrObjectTooLarge
		return 0, ErrObjectTooLarge
	}
	written, err := writer.writer.Write(value)
	writer.writeErr = err
	writer.written += int64(written)
	return written, err
}

type verifyingReader struct {
	body     io.ReadCloser
	digest   hash.Hash
	ctxErr   func() error
	expected string
	size     int64
	read     int64
	verified bool
}

func (reader *verifyingReader) Read(value []byte) (int, error) {
	if err := reader.ctxErr(); err != nil {
		return 0, err
	}
	read, err := reader.body.Read(value)
	if read > 0 {
		_, _ = reader.digest.Write(value[:read])
		reader.read += int64(read)
	}
	if errors.Is(err, io.EOF) && !reader.verified {
		reader.verified = true
		if reader.read != reader.size || digestValue(reader.digest) != reader.expected {
			return read, ErrIntegrity
		}
	}
	return read, err
}

func (reader *verifyingReader) Close() error {
	if err := reader.body.Close(); err != nil {
		return ErrUnavailable
	}
	return nil
}
