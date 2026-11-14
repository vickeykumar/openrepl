package objstore

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Usage is what the store holds under the client's prefix, counted from the store's own listing.
type Usage struct {
	Bytes         int64 // the objects as they are now
	Objects       int
	OlderBytes    int64 // the versions an overwrite or a delete left behind, which a versioned bucket (B2) keeps and bills; 0 when it keeps none
	OlderVersions int
	VersionsKnown bool // the store listed versions: OlderBytes is a count, not a guess
	Truncated     bool // the bucket is bigger than the pages that were read: the numbers are at least these
}

// the pages a count reads at most (1000 objects a page), so that a huge bucket cannot hold the dashboard
const usageMaxPages = 300

// Usage counts the objects under the prefix by reading the listing page by page, and adds up what is kept of older versions when the
// store can list them. Nothing is kept in memory but the sums.
func (c *Client) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	token := ""
	for page := 0; ; page++ {
		if page >= usageMaxPages {
			u.Truncated = true
			break
		}
		q := url.Values{"list-type": {"2"}, "prefix": {c.full("")}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.do(ctx, http.MethodGet, c.url("", q), nil, nil)
		if err != nil {
			return u, err
		}
		if resp.StatusCode/100 != 2 {
			return u, readError(resp)
		}
		var p struct {
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
			Contents  []struct {
				Size int64 `xml:"Size"`
			} `xml:"Contents"`
		}
		err = xml.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if err != nil {
			return u, fmt.Errorf("objstore: the listing could not be read: %v", err)
		}
		for _, o := range p.Contents {
			u.Bytes += o.Size
			u.Objects++
		}
		if !p.Truncated || p.Next == "" {
			break
		}
		token = p.Next
	}
	// the older versions: a store that does not list them has none to count
	if err := c.olderVersions(ctx, &u); err != nil && !errors.Is(err, ErrNoVersions) {
		return u, err
	}
	return u, nil
}

func (c *Client) olderVersions(ctx context.Context, u *Usage) error {
	keyMarker, idMarker := "", ""
	for page := 0; ; page++ {
		if page >= usageMaxPages {
			u.Truncated = true
			return nil
		}
		q := url.Values{"versions": {""}, "prefix": {c.full("")}}
		if keyMarker != "" {
			q.Set("key-marker", keyMarker)
			if idMarker != "" {
				q.Set("version-id-marker", idMarker)
			}
		}
		resp, err := c.do(ctx, http.MethodGet, c.url("", q), nil, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusBadRequest {
			resp.Body.Close()
			return ErrNoVersions
		}
		if resp.StatusCode/100 != 2 {
			return readError(resp)
		}
		var p struct {
			Truncated bool   `xml:"IsTruncated"`
			NextKey   string `xml:"NextKeyMarker"`
			NextID    string `xml:"NextVersionIdMarker"`
			Versions  []struct {
				IsLatest bool  `xml:"IsLatest"`
				Size     int64 `xml:"Size"`
			} `xml:"Version"`
		}
		err = xml.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("objstore: the listing of versions could not be read: %v", err)
		}
		u.VersionsKnown = true
		for _, v := range p.Versions {
			if !v.IsLatest {
				u.OlderBytes += v.Size
				u.OlderVersions++
			}
		}
		if !p.Truncated || p.NextKey == "" {
			return nil
		}
		keyMarker, idMarker = p.NextKey, p.NextID
	}
}
