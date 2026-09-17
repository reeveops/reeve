package s3

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/reeveops/reeve/internal/blob"
)

// responseError builds the layered error the SDK produces for an HTTP
// failure: OperationError > ResponseError > (optional) APIError.
func responseError(status int, inner error) error {
	return &smithy.OperationError{
		ServiceID:     "S3",
		OperationName: "PutObject",
		Err: &awshttp.ResponseError{
			ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{
					Response: &http.Response{StatusCode: status},
				},
				Err: inner,
			},
		},
	}
}

func TestIsPreconditionFailed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "412 PreconditionFailed api error",
			err: &smithy.GenericAPIError{
				Code:    "PreconditionFailed",
				Message: "At least one of the pre-conditions you specified did not hold",
			},
			want: true,
		},
		{
			name: "409 ConditionalRequestConflict api error",
			err: &smithy.GenericAPIError{
				Code:    "ConditionalRequestConflict",
				Message: "A conflicting conditional operation is currently in progress against this resource.",
			},
			want: true,
		},
		{
			name: "412 wrapped in operation + response error",
			err: responseError(http.StatusPreconditionFailed, &smithy.GenericAPIError{
				Code: "PreconditionFailed",
			}),
			want: true,
		},
		{
			name: "409 wrapped in operation + response error",
			err: responseError(http.StatusConflict, &smithy.GenericAPIError{
				Code: "ConditionalRequestConflict",
			}),
			want: true,
		},
		{
			name: "bare 412 response error without api error code",
			err:  responseError(http.StatusPreconditionFailed, errors.New("precondition failed")),
			want: true,
		},
		{
			name: "fmt-wrapped api error",
			err: fmt.Errorf("put lock blob: %w", &smithy.GenericAPIError{
				Code: "ConditionalRequestConflict",
			}),
			want: true,
		},
		{
			name: "unrelated 409 OperationAborted is not a CAS conflict",
			err:  responseError(http.StatusConflict, &smithy.GenericAPIError{Code: "OperationAborted"}),
			want: false,
		},
		{
			name: "no such key",
			err:  &s3types.NoSuchKey{},
			want: false,
		},
		{
			name: "arbitrary error",
			err:  errors.New("connection reset by peer"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPreconditionFailed(tc.err); got != tc.want {
				t.Fatalf("isPreconditionFailed(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"NoSuchKey typed", &s3types.NoSuchKey{}, true},
		{"NoSuchBucket typed", &s3types.NoSuchBucket{}, true},
		{"NotFound api error", &smithy.GenericAPIError{Code: "NotFound"}, true},
		{"wrapped NoSuchKey", fmt.Errorf("get: %w", &s3types.NoSuchKey{}), true},
		{"precondition failure is not not-found", &smithy.GenericAPIError{Code: "PreconditionFailed"}, false},
		{"arbitrary error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFound(tc.err); got != tc.want {
				t.Fatalf("isNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDeleteIfMatchRejectsEndpointThatIgnoresPrecondition(t *testing.T) {
	t.Parallel()

	var puts atomic.Int64
	var mu sync.Mutex
	var deletePaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.Method {
		case http.MethodPut:
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, puts.Add(1)))
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			mu.Lock()
			deletePaths = append(deletePaths, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, err := New(t.Context(), Options{
		Bucket:       "contract",
		Region:       "us-east-1",
		Endpoint:     server.URL,
		UsePathStyle: true,
		AccessKey:    "test",
		SecretKey:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteIfMatch(t.Context(), "runs/user.json", "user-etag"); !errors.Is(err, blob.ErrConditionalDeletesUnsupported) {
		t.Fatalf("DeleteIfMatch = %v, want ErrConditionalDeletesUnsupported", err)
	}
	if got := puts.Load(); got != 2 {
		t.Fatalf("probe writes = %d, want 2", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, requestPath := range deletePaths {
		if strings.HasSuffix(requestPath, "/runs/user.json") {
			t.Fatalf("unsafe endpoint received deletion for user object: %q", requestPath)
		}
	}
}

func TestDeleteIfMatchCachesProbeFailure(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	var mu sync.Mutex
	var putPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodPut {
			mu.Lock()
			putPath = r.URL.Path
			mu.Unlock()
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	store, err := New(t.Context(), Options{
		Bucket:       "contract",
		Region:       "us-east-1",
		Endpoint:     server.URL,
		UsePathStyle: true,
		AccessKey:    "test",
		SecretKey:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstErr := store.DeleteIfMatch(t.Context(), "runs/one.json", "etag")
	if !errors.Is(firstErr, blob.ErrConditionalDeleteProbeFailed) {
		t.Fatalf("first DeleteIfMatch = %v, want ErrConditionalDeleteProbeFailed", firstErr)
	}
	firstRequests := requests.Load()
	secondErr := store.DeleteIfMatch(t.Context(), "runs/two.json", "etag")
	if !errors.Is(secondErr, blob.ErrConditionalDeleteProbeFailed) {
		t.Fatalf("second DeleteIfMatch = %v, want ErrConditionalDeleteProbeFailed", secondErr)
	}
	if got := requests.Load(); got != firstRequests {
		t.Fatalf("cached probe made more requests: first=%d total=%d", firstRequests, got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(putPath, "/runs/.delete-cas-probe/") {
		t.Fatalf("probe path = %q, want managed runs namespace", putPath)
	}
}
