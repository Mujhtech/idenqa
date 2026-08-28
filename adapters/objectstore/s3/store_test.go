package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestStoreWritesReadsAndDeletesExactVersions(t *testing.T) {
	t.Parallel()

	client := newMemoryClient()
	store := mustStore(t, client, bytes.NewReader(append(
		bytes.Repeat([]byte{0x2a}, 16), bytes.Repeat([]byte{0x2b}, 16)...,
	)))
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	firstPayload := []byte("first authenticated ciphertext")
	first := putBytes(t, store, key, firstPayload)
	secondPayload := []byte("second authenticated ciphertext")
	second := putBytes(t, store, key, secondPayload)
	if first.Record().Version == second.Record().Version {
		t.Fatal("Put() reused an object version")
	}
	if got := client.lastIfNoneMatch; got != "*" {
		t.Fatalf("If-None-Match = %q, want *", got)
	}
	if !strings.HasPrefix(client.lastKey, "idenqa/prefix/"+string(key)+"/") {
		t.Fatalf("physical key = %q", client.lastKey)
	}
	assertRead(t, store, first, firstPayload)
	assertRead(t, store, second, secondPayload)
	if err := store.Delete(context.Background(), first); err != nil {
		t.Fatalf("Delete(first) error = %v", err)
	}
	if err := store.Delete(context.Background(), first); err != nil {
		t.Fatalf("Delete(first replay) error = %v", err)
	}
	if _, err := store.Open(context.Background(), first); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open(deleted first) error = %v, want ErrUnavailable", err)
	}
	assertRead(t, store, second, secondPayload)
}

func TestStoreStreamsThroughRealAWSSDKClient(t *testing.T) {
	for _, secure := range []bool{true, false} {
		name := "https"
		if !secure {
			name = "explicit-http"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := newS3TestServer(t, secure)
			defer server.Close()
			awsConfig := aws.Config{
				Region: "us-east-1",
				Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
					"test-access-key", "test-secret-key", "",
				)),
				HTTPClient:                 server.Client(),
				RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
				ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
			}
			client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
				options.BaseEndpoint = aws.String(server.URL)
				options.UsePathStyle = true
			})
			config := validConfig(server.URL)
			config.AllowHTTP = !secure
			store, err := New(client, config)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
			payload := bytes.Repeat([]byte("authenticated ciphertext"), 4_096)
			object := putBytes(t, store, key, payload)
			assertRead(t, store, object, payload)
			page, err := store.ListInventory(
				context.Background(), mustKey(t, "tenants/ten_1/evidence"), "", 10,
			)
			if err != nil || len(page.Objects()) != 1 ||
				page.Objects()[0].Record().Key != object.Record().Key {
				t.Fatalf("ListInventory() objects=%+v error=%v", page.Objects(), err)
			}
			if err := store.Delete(context.Background(), object); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := store.Open(context.Background(), object); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Open(deleted) error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestStoreRemovesFailedAndOversizedUploads(t *testing.T) {
	t.Parallel()

	client := newMemoryClient()
	config := validConfig("https://objects.example.test")
	config.MaxObjectBytes = 8
	store, err := newStore(client, config, bytes.NewReader(bytes.Repeat([]byte{0x44}, 64)))
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	if object, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		_, err := writer.Write([]byte("too-large"))
		return err
	}); !object.IsZero() || !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("Put(oversized) object=%+v error=%v", object.Record(), err)
	}
	producerErr := errors.New("encryption failed")
	if object, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		if _, err := writer.Write([]byte("partial")); err != nil {
			return err
		}
		return producerErr
	}); !object.IsZero() || !errors.Is(err, producerErr) {
		t.Fatalf("Put(producer failure) object=%+v error=%v", object.Record(), err)
	}
	if client.objectCount() != 0 {
		t.Fatalf("failed uploads left %d objects", client.objectCount())
	}
}

func TestStoreReturnsExactReferenceWhenAmbiguousCleanupFails(t *testing.T) {
	t.Parallel()

	client := &functionClient{
		put: func(_ context.Context, input *awss3.PutObjectInput) (*awss3.PutObjectOutput, error) {
			_, _ = io.Copy(io.Discard, input.Body)
			return nil, errors.New("connection reset after upload")
		},
		delete: func(context.Context, *awss3.DeleteObjectInput) (*awss3.DeleteObjectOutput, error) {
			return nil, errors.New("cleanup unavailable")
		},
	}
	store := mustStore(t, client, bytes.NewReader(bytes.Repeat([]byte{0x51}, 32)))
	object, err := store.Put(
		context.Background(), mustKey(t, "tenants/ten_1/evidence/evd_1/content/1"),
		func(writer io.Writer) error {
			_, err := writer.Write([]byte("ciphertext"))
			return err
		},
	)
	if object.IsZero() || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put() object=%+v error=%v, want exact reference and ErrUnavailable", object.Record(), err)
	}
}

func TestStoreNeverDeletesAnotherWriterAfterPreconditionFailure(t *testing.T) {
	t.Parallel()

	deleted := false
	client := &functionClient{
		put: func(context.Context, *awss3.PutObjectInput) (*awss3.PutObjectOutput, error) {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "exists"}
		},
		delete: func(context.Context, *awss3.DeleteObjectInput) (*awss3.DeleteObjectOutput, error) {
			deleted = true
			return &awss3.DeleteObjectOutput{}, nil
		},
	}
	store := mustStore(t, client, bytes.NewReader(bytes.Repeat([]byte{0x62}, 32)))
	object, err := store.Put(
		context.Background(), mustKey(t, "tenants/ten_1/evidence/evd_1/content/1"),
		func(writer io.Writer) error {
			_, err := writer.Write([]byte("ciphertext"))
			return err
		},
	)
	if !object.IsZero() || !errors.Is(err, ErrUnavailable) || deleted {
		t.Fatalf("Put(precondition) object=%+v error=%v deleted=%t", object.Record(), err, deleted)
	}
}

func TestStoreDetectsCiphertextModification(t *testing.T) {
	t.Parallel()

	client := newMemoryClient()
	store := mustStore(t, client, bytes.NewReader(bytes.Repeat([]byte{0x73}, 32)))
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	object := putBytes(t, store, key, []byte("authenticated ciphertext"))
	client.tamper(store.physicalKey(key, object.Record().Version))
	reader, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatalf("Open(tampered) error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.ReadAll(reader); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ReadAll(tampered) error = %v, want ErrIntegrity", err)
	}
}

func TestStoreInventoriesAndDeletesExactPhysicalObjects(t *testing.T) {
	t.Parallel()

	client := newMemoryClient()
	store := mustStore(t, client, bytes.NewReader(append(
		append(bytes.Repeat([]byte{0x76}, 16), bytes.Repeat([]byte{0x77}, 16)...),
		bytes.Repeat([]byte{0x78}, 16)...,
	)))
	prefix := mustKey(t, "tenants/ten_1/evidence")
	first := putBytes(t, store, mustKey(t, string(prefix)+"/evd_1/content/1"), []byte("first"))
	second := putBytes(t, store, mustKey(t, string(prefix)+"/evd_2/content/1"), []byte("second"))
	_ = putBytes(t, store, mustKey(t, "tenants/ten_2/evidence/evd_3/content/1"), []byte("peer"))

	page, err := store.ListInventory(context.Background(), prefix, "", 1)
	if err != nil {
		t.Fatalf("ListInventory(first page) error = %v", err)
	}
	if len(page.Objects()) != 1 || page.Next() == "" {
		t.Fatalf("first page objects=%d next=%q", len(page.Objects()), page.Next())
	}
	next, err := store.ListInventory(context.Background(), prefix, page.Next(), 1)
	if err != nil {
		t.Fatalf("ListInventory(second page) error = %v", err)
	}
	if len(next.Objects()) != 1 || next.Next() != "" {
		t.Fatalf("second page objects=%d next=%q", len(next.Objects()), next.Next())
	}
	discovered := append(page.Objects(), next.Objects()...)
	if discovered[0].Record().Key != first.Record().Key ||
		discovered[1].Record().Key != second.Record().Key {
		t.Fatalf("inventory keys = %q, %q", discovered[0].Record().Key, discovered[1].Record().Key)
	}
	if err := store.DeleteInventory(context.Background(), discovered[0]); err != nil {
		t.Fatalf("DeleteInventory() error = %v", err)
	}
	if err := store.DeleteInventory(context.Background(), discovered[0]); err != nil {
		t.Fatalf("DeleteInventory(replay) error = %v", err)
	}
	if _, err := store.Open(context.Background(), first); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open(deleted inventory object) error = %v", err)
	}
	assertRead(t, store, second, []byte("second"))
}

func TestStoreValidatesConfigurationAndCancellation(t *testing.T) {
	t.Parallel()

	client := newMemoryClient()
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "missing bucket", change: func(config *Config) { config.Bucket = "" }},
		{name: "unsafe prefix", change: func(config *Config) { config.Prefix = "../peer" }},
		{name: "insecure endpoint", change: func(config *Config) { config.Endpoint = "http://objects.example.test" }},
		{name: "endpoint credentials", change: func(config *Config) { config.Endpoint = "https://user:secret@objects.example.test" }},
		{name: "oversized bound", change: func(config *Config) { config.MaxObjectBytes = maxSinglePutBytes + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig("https://objects.example.test")
			test.change(&config)
			if _, err := New(client, config); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("New() error = %v, want ErrUnavailable", err)
			}
		})
	}
	config := validConfig("http://127.0.0.1:9000")
	config.AllowHTTP = true
	if _, err := New(client, config); err != nil {
		t.Fatalf("New(explicit development HTTP) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := mustStore(t, client, bytes.NewReader(bytes.Repeat([]byte{0x84}, 32)))
	if _, err := store.Put(ctx, mustKey(t, "tenant/evidence/content"), func(io.Writer) error {
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(cancelled) error = %v, want context.Canceled", err)
	}
}

func validConfig(endpoint string) Config {
	return Config{
		Bucket: "idenqa-evidence", Prefix: "idenqa/prefix", Region: "us-east-1",
		Endpoint: endpoint, ForcePathStyle: true, MaxObjectBytes: 2 * 1024 * 1024,
		CleanupTimeout: time.Second,
	}
}

func mustStore(t *testing.T, client Client, random io.Reader) *Store {
	t.Helper()
	store, err := newStore(client, validConfig("https://objects.example.test"), random)
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	return store
}

func mustKey(t *testing.T, value string) objectstore.Key {
	t.Helper()
	key, err := objectstore.NewKey(value)
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}
	return key
}

func putBytes(t *testing.T, store *Store, key objectstore.Key, value []byte) objectstore.Object {
	t.Helper()
	object, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		_, err := writer.Write(value)
		return err
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	return object
}

func assertRead(t *testing.T, store *Store, object objectstore.Object, want []byte) {
	t.Helper()
	reader, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadAll() length = %d, want %d", len(got), len(want))
	}
}

type functionClient struct {
	put    func(context.Context, *awss3.PutObjectInput) (*awss3.PutObjectOutput, error)
	get    func(context.Context, *awss3.GetObjectInput) (*awss3.GetObjectOutput, error)
	delete func(context.Context, *awss3.DeleteObjectInput) (*awss3.DeleteObjectOutput, error)
	list   func(context.Context, *awss3.ListObjectsV2Input) (*awss3.ListObjectsV2Output, error)
}

func (client *functionClient) PutObject(
	ctx context.Context,
	input *awss3.PutObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.PutObjectOutput, error) {
	return client.put(ctx, input)
}

func (client *functionClient) GetObject(
	ctx context.Context,
	input *awss3.GetObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.GetObjectOutput, error) {
	if client.get == nil {
		return nil, errors.New("not found")
	}
	return client.get(ctx, input)
}

func (client *functionClient) DeleteObject(
	ctx context.Context,
	input *awss3.DeleteObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.DeleteObjectOutput, error) {
	return client.delete(ctx, input)
}

func (client *functionClient) ListObjectsV2(
	ctx context.Context,
	input *awss3.ListObjectsV2Input,
	_ ...func(*awss3.Options),
) (*awss3.ListObjectsV2Output, error) {
	if client.list == nil {
		return nil, errors.New("listing unavailable")
	}
	return client.list(ctx, input)
}

type memoryClient struct {
	mutex           sync.Mutex
	objects         map[string][]byte
	modified        map[string]time.Time
	lastKey         string
	lastIfNoneMatch string
}

func newMemoryClient() *memoryClient {
	return &memoryClient{objects: make(map[string][]byte), modified: make(map[string]time.Time)}
}

func (client *memoryClient) PutObject(
	_ context.Context,
	input *awss3.PutObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.PutObjectOutput, error) {
	value, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.lastKey = aws.ToString(input.Key)
	client.lastIfNoneMatch = aws.ToString(input.IfNoneMatch)
	if _, exists := client.objects[client.lastKey]; exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "exists"}
	}
	client.objects[client.lastKey] = bytes.Clone(value)
	client.modified[client.lastKey] = time.Now().UTC()
	return &awss3.PutObjectOutput{}, nil
}

func (client *memoryClient) GetObject(
	_ context.Context,
	input *awss3.GetObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.GetObjectOutput, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	value, exists := client.objects[aws.ToString(input.Key)]
	if !exists {
		return nil, errors.New("not found")
	}
	size := int64(len(value))
	return &awss3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(bytes.Clone(value))), ContentLength: &size,
	}, nil
}

func (client *memoryClient) DeleteObject(
	_ context.Context,
	input *awss3.DeleteObjectInput,
	_ ...func(*awss3.Options),
) (*awss3.DeleteObjectOutput, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	delete(client.objects, aws.ToString(input.Key))
	delete(client.modified, aws.ToString(input.Key))
	return &awss3.DeleteObjectOutput{}, nil
}

func (client *memoryClient) ListObjectsV2(
	_ context.Context,
	input *awss3.ListObjectsV2Input,
	_ ...func(*awss3.Options),
) (*awss3.ListObjectsV2Output, error) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	keys := make([]string, 0, len(client.objects))
	for key := range client.objects {
		if strings.HasPrefix(key, aws.ToString(input.Prefix)) &&
			(aws.ToString(input.ContinuationToken) == "" || key > aws.ToString(input.ContinuationToken)) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	maximum := int(aws.ToInt32(input.MaxKeys))
	isTruncated := len(keys) > maximum
	if isTruncated {
		keys = keys[:maximum]
	}
	contents := make([]awstypes.Object, 0, len(keys))
	for _, key := range keys {
		size := int64(len(client.objects[key]))
		modifiedAt := client.modified[key]
		contents = append(contents, awstypes.Object{Key: aws.String(key), Size: &size, LastModified: &modifiedAt})
	}
	output := &awss3.ListObjectsV2Output{Contents: contents, IsTruncated: aws.Bool(isTruncated)}
	if isTruncated {
		output.NextContinuationToken = aws.String(keys[len(keys)-1])
	}
	return output, nil
}

func (client *memoryClient) objectCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return len(client.objects)
}

func (client *memoryClient) tamper(key string) {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.objects[key][0] ^= 0xff
}

func newS3TestServer(t *testing.T, secure bool) *httptest.Server {
	t.Helper()
	var mutex sync.Mutex
	objects := make(map[string][]byte)
	modified := make(map[string]time.Time)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		switch request.Method {
		case http.MethodPut:
			if request.Header.Get("If-None-Match") != "*" {
				http.Error(writer, "precondition required", http.StatusPreconditionRequired)
				return
			}
			if _, exists := objects[request.URL.Path]; exists {
				http.Error(writer, "exists", http.StatusPreconditionFailed)
				return
			}
			value, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(writer, "read", http.StatusBadRequest)
				return
			}
			objects[request.URL.Path] = value
			modified[request.URL.Path] = time.Now().UTC()
			writer.Header().Set("ETag", `"test"`)
			writer.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if request.URL.Query().Get("list-type") == "2" {
				prefix := request.URL.Query().Get("prefix")
				contents := make([]s3ListObject, 0, len(objects))
				for objectPath, value := range objects {
					key := strings.TrimPrefix(objectPath, "/idenqa-evidence/")
					if strings.HasPrefix(key, prefix) {
						contents = append(contents, s3ListObject{
							Key: key, LastModified: modified[objectPath], Size: int64(len(value)),
						})
					}
				}
				sort.Slice(contents, func(left, right int) bool {
					return contents[left].Key < contents[right].Key
				})
				writer.Header().Set("Content-Type", "application/xml")
				if err := xml.NewEncoder(writer).Encode(s3ListResult{
					XMLNS: "http://s3.amazonaws.com/doc/2006-03-01/",
					Name:  "idenqa-evidence", Prefix: prefix, KeyCount: len(contents),
					MaxKeys: 1000, IsTruncated: false, Contents: contents,
				}); err != nil {
					t.Errorf("encode S3 list response: %v", err)
				}
				return
			}
			value, exists := objects[request.URL.Path]
			if !exists {
				http.Error(writer, "not found", http.StatusNotFound)
				return
			}
			writer.Header().Set("Content-Length", strconv.Itoa(len(value)))
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(value)
		case http.MethodDelete:
			delete(objects, request.URL.Path)
			delete(modified, request.URL.Path)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.Error(writer, "unsupported", http.StatusMethodNotAllowed)
		}
	})
	if secure {
		return httptest.NewTLSServer(handler)
	}
	return httptest.NewServer(handler)
}

type s3ListResult struct {
	XMLName     xml.Name       `xml:"ListBucketResult"`
	XMLNS       string         `xml:"xmlns,attr"`
	Name        string         `xml:"Name"`
	Prefix      string         `xml:"Prefix"`
	KeyCount    int            `xml:"KeyCount"`
	MaxKeys     int            `xml:"MaxKeys"`
	IsTruncated bool           `xml:"IsTruncated"`
	Contents    []s3ListObject `xml:"Contents"`
}

type s3ListObject struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	Size         int64     `xml:"Size"`
}
