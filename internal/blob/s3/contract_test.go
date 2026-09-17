package s3

import (
	"os"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/blobtest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	bucket := os.Getenv("REEVE_S3_CONTRACT_BUCKET")
	if bucket == "" {
		t.Skip("REEVE_S3_CONTRACT_BUCKET is not set")
	}
	basePrefix := os.Getenv("REEVE_S3_CONTRACT_PREFIX")
	blobtest.RunContract(t, blobtest.Subject{
		ExpectConditionalDeletesUnsupported: os.Getenv("REEVE_S3_CONTRACT_EXPECT_CONDITIONAL_DELETE_UNSUPPORTED") == "true",
		NewStore: func(t *testing.T) blob.Store {
			t.Helper()
			endpoint := os.Getenv("REEVE_S3_CONTRACT_ENDPOINT")
			store, err := New(t.Context(), Options{
				Bucket:       bucket,
				Region:       os.Getenv("REEVE_S3_CONTRACT_REGION"),
				Prefix:       blobtest.IsolatedPrefix(t, basePrefix),
				Endpoint:     endpoint,
				UsePathStyle: endpoint != "",
			})
			if err != nil {
				t.Fatalf("new S3 contract store: %v", err)
			}
			return store
		},
	})
}
