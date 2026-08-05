package azblob

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

// respErr builds the error shape the Azure SDK produces for a service
// failure: *azcore.ResponseError carrying the x-ms-error-code and HTTP
// status.
func respErr(code bloberror.Code, status int) error {
	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{"X-Ms-Error-Code": []string{string(code)}},
		Request: &http.Request{
			Method: http.MethodPut,
			URL:    &url.URL{Scheme: "https", Host: "example.invalid", Path: "/state/key"},
		},
	}
	return &azcore.ResponseError{
		ErrorCode:   string(code),
		StatusCode:  status,
		RawResponse: resp,
	}
}

func TestIsPreconditionFailed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ConditionNotMet 412 response error",
			err:  respErr(bloberror.ConditionNotMet, http.StatusPreconditionFailed),
			want: true,
		},
		{
			name: "BlobAlreadyExists 409 is not a CAS conflict here",
			// If-None-Match:* failures surface as 409 BlobAlreadyExists;
			// today only 412/ConditionNotMet is classified. Pin that.
			err:  respErr(bloberror.BlobAlreadyExists, http.StatusConflict),
			want: false,
		},
		{
			name: "bare 412 response error without error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed},
			want: true,
		},
		{
			name: "wrapped 412",
			err:  fmt.Errorf("put lock blob: %w", respErr(bloberror.ConditionNotMet, http.StatusPreconditionFailed)),
			want: true,
		},
		{
			name: "stringified ConditionNotMet fallback",
			err:  errors.New("RESPONSE 412: ERROR CODE: ConditionNotMet"),
			want: true,
		},
		{
			name: "404 not found is not a precondition failure",
			err:  respErr(bloberror.BlobNotFound, http.StatusNotFound),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
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
				t.Fatalf("isPreconditionFailed = %v, want %v", got, tc.want)
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
		{"BlobNotFound 404", respErr(bloberror.BlobNotFound, http.StatusNotFound), true},
		{"ContainerNotFound 404 (status match)", respErr(bloberror.ContainerNotFound, http.StatusNotFound), true},
		{"bare 404 response error", &azcore.ResponseError{StatusCode: http.StatusNotFound}, true},
		{"wrapped BlobNotFound", fmt.Errorf("get: %w", respErr(bloberror.BlobNotFound, http.StatusNotFound)), true},
		{"stringified BlobNotFound fallback", errors.New("RESPONSE 404: ERROR CODE: BlobNotFound"), true},
		{"412 is not not-found", respErr(bloberror.ConditionNotMet, http.StatusPreconditionFailed), false},
		{"nil", nil, false},
		{"arbitrary error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFound(tc.err); got != tc.want {
				t.Fatalf("isNotFound = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFullKeyPrefixHandling(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		key    string
		want   string
	}{
		{"no prefix", "", "locks/a.json", "locks/a.json"},
		{"prefix already slashed", "team/", "locks/a.json", "team/locks/a.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{prefix: tc.prefix}
			if got := s.fullKey(tc.key); got != tc.want {
				t.Errorf("fullKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestNewNormalizesPrefix exercises the constructor. DefaultAzureCredential
// construction is local-only (no token is requested until a call is made),
// and the client never dials during New.
func TestNewNormalizesPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{"empty stays empty", "", ""},
		{"missing slash appended", "team", "team/"},
		{"existing slash kept", "team/", "team/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(t.Context(), Options{
				ServiceURL: "https://example.invalid",
				Container:  "state",
				Prefix:     tc.prefix,
			})
			if err != nil {
				if strings.Contains(err.Error(), "DefaultAzureCredential") {
					t.Skipf("no credential chain available in this environment: %v", err)
				}
				t.Fatal(err)
			}
			if s.prefix != tc.want {
				t.Errorf("prefix = %q, want %q", s.prefix, tc.want)
			}
			if s.container != "state" {
				t.Errorf("container = %q", s.container)
			}
		})
	}
}
