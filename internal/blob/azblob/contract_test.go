package azblob

import (
	"os"
	"testing"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/blob/blobtest"
)

func TestContract(t *testing.T) {
	t.Parallel()
	container := os.Getenv("REEVE_AZURE_CONTRACT_CONTAINER")
	serviceURL := os.Getenv("REEVE_AZURE_CONTRACT_SERVICE_URL")
	if container == "" || serviceURL == "" {
		t.Skip("REEVE_AZURE_CONTRACT_CONTAINER and REEVE_AZURE_CONTRACT_SERVICE_URL are not set")
	}
	basePrefix := os.Getenv("REEVE_AZURE_CONTRACT_PREFIX")
	blobtest.RunContract(t, blobtest.Subject{
		ExpectConditionalDeletesUnsupported: os.Getenv("REEVE_AZURE_CONTRACT_EXPECT_CONDITIONAL_DELETE_UNSUPPORTED") == "true",
		NewStore: func(t *testing.T) blob.Store {
			t.Helper()
			store, err := New(t.Context(), Options{
				ServiceURL: serviceURL,
				Container:  container,
				Prefix:     blobtest.IsolatedPrefix(t, basePrefix),
			})
			if err != nil {
				t.Fatalf("new Azure Blob contract store: %v", err)
			}
			return store
		},
	})
}
