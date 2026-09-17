package gcs

import (
	"os"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/blobtest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	bucket := os.Getenv("REEVE_GCS_CONTRACT_BUCKET")
	if bucket == "" {
		t.Skip("REEVE_GCS_CONTRACT_BUCKET is not set")
	}
	basePrefix := os.Getenv("REEVE_GCS_CONTRACT_PREFIX")
	blobtest.RunContract(t, blobtest.Subject{
		ExpectConditionalDeletesUnsupported: os.Getenv("REEVE_GCS_CONTRACT_EXPECT_CONDITIONAL_DELETE_UNSUPPORTED") == "true",
		NewStore: func(t *testing.T) blob.Store {
			t.Helper()
			store, err := New(t.Context(), Options{
				Bucket: bucket,
				Prefix: blobtest.IsolatedPrefix(t, basePrefix),
			})
			if err != nil {
				t.Fatalf("new GCS contract store: %v", err)
			}
			return store
		},
	})
}
