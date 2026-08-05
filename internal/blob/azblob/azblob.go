// Package azblob is the Azure Blob Storage adapter. Atomic writes via
// If-Match on ETag (or If-None-Match:* for absence). Uses
// DefaultAzureCredential from the environment.
package azblob

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	az "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	reeveblob "github.com/FynxLabs/reeve/internal/blob"
)

// Store implements reeveblob.Store against an Azure Blob container.
type Store struct {
	client    *az.Client
	container string
	prefix    string
}

// Options configures New.
type Options struct {
	ServiceURL string // e.g. "https://<account>.blob.core.windows.net"
	Container  string
	Prefix     string
}

// New returns a Store using DefaultAzureCredential.
func New(ctx context.Context, opts Options) (*Store, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	c, err := az.NewClient(opts.ServiceURL, cred, nil)
	if err != nil {
		return nil, err
	}
	prefix := opts.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &Store{client: c, container: opts.Container, prefix: prefix}, nil
}

func (s *Store) fullKey(k string) string { return s.prefix + k }

// Get reads an object.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, *reeveblob.Metadata, error) {
	blobCli := s.client.ServiceClient().NewContainerClient(s.container).NewBlobClient(s.fullKey(key))
	resp, err := blobCli.DownloadStream(ctx, nil)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, reeveblob.ErrNotFound
		}
		return nil, nil, err
	}
	md := &reeveblob.Metadata{}
	if resp.ETag != nil {
		md.ETag = strings.Trim(string(*resp.ETag), `"`)
	}
	if resp.LastModified != nil {
		md.LastModified = resp.LastModified.Unix()
	}
	if resp.ContentLength != nil {
		md.Size = *resp.ContentLength
	}
	return resp.Body, md, nil
}

// Put writes unconditionally.
func (s *Store) Put(ctx context.Context, key string, r io.Reader) (*reeveblob.Metadata, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.UploadBuffer(ctx, s.container, s.fullKey(key), buf, nil)
	if err != nil {
		return nil, err
	}
	md := &reeveblob.Metadata{Size: int64(len(buf))}
	if resp.ETag != nil {
		md.ETag = strings.Trim(string(*resp.ETag), `"`)
	}
	return md, nil
}

// PutIfMatch uses If-Match / If-None-Match:* via AccessConditions.
func (s *Store) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*reeveblob.Metadata, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	conds := &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{}}
	if ifMatch == "" {
		star := azcore.ETag("*")
		conds.ModifiedAccessConditions.IfNoneMatch = &star
	} else {
		etag := azcore.ETag(`"` + ifMatch + `"`)
		conds.ModifiedAccessConditions.IfMatch = &etag
	}

	resp, err := s.client.UploadBuffer(ctx, s.container, s.fullKey(key), buf, &az.UploadBufferOptions{
		AccessConditions: conds,
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return nil, reeveblob.ErrPreconditionFailed
		}
		return nil, err
	}
	md := &reeveblob.Metadata{Size: int64(len(buf))}
	if resp.ETag != nil {
		md.ETag = strings.Trim(string(*resp.ETag), `"`)
	}
	return md, nil
}

// Delete removes an object. Missing is silent.
func (s *Store) Delete(ctx context.Context, key string) error {
	blobCli := s.client.ServiceClient().NewContainerClient(s.container).NewBlobClient(s.fullKey(key))
	_, err := blobCli.Delete(ctx, nil)
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// List returns keys under the prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	fullPrefix := s.fullKey(prefix)
	pager := s.client.ServiceClient().NewContainerClient(s.container).NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: &fullPrefix,
	})
	var out []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		if page.Segment == nil {
			continue
		}
		for _, b := range page.Segment.BlobItems {
			if b.Name == nil {
				continue
			}
			out = append(out, strings.TrimPrefix(*b.Name, s.prefix))
		}
	}
	return out, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return true
	}
	var terr *azcore.ResponseError
	if errors.As(err, &terr) && terr.StatusCode == 404 {
		return true
	}
	return strings.Contains(err.Error(), "BlobNotFound") || strings.Contains(err.Error(), "404")
}

func isPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	var terr *azcore.ResponseError
	if errors.As(err, &terr) && terr.StatusCode == 412 {
		return true
	}
	if bloberror.HasCode(err, bloberror.ConditionNotMet) {
		return true
	}
	return strings.Contains(err.Error(), "ConditionNotMet") || strings.Contains(err.Error(), "412")
}

// compile-time check
var _ reeveblob.Store = (*Store)(nil)
