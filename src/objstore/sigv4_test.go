package objstore

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The example of the AWS documentation, "Example: GET Object" of "Signature Calculations for the
// Authorization Header": the signature of this request is known.
func TestSignatureOfTheAWSDocumentationExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	sign(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", emptySHA256, now)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date," +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization:\n got %s\nwant %s", got, want)
	}
}

func TestKeysAreEncodedTheWayS3WantsThem(t *testing.T) {
	for in, want := range map[string]string{
		"a/b c.txt":            "a/b%20c.txt",
		"homes/é/x":            "homes/%C3%A9/x",
		"a+b=c&d":              "a%2Bb%3Dc%26d",
		"~-_.":                 "~-_.",
		"2026-11-01T14:30:00Z": "2026-11-01T14%3A30%3A00Z",
	} {
		if got := uriEncode(in, true); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := uriEncode("a/b", false); got != "a%2Fb" {
		t.Errorf("a slash that is not part of a path: %q", got)
	}
	if !strings.Contains(canonicalQuery(map[string][]string{"prefix": {"a b"}, "list-type": {"2"}}), "list-type=2&prefix=a%20b") {
		t.Error("the query is sorted and encoded")
	}
}
