package appstore

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// Merging nothing changes only the layout of the file (the field order of the entries), never what it says; and a second merge changes nothing.
func TestMergeKeepsTheContent(t *testing.T) {
	once, err := MergeMeasured(defaultCatalogueJSON)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	json.Unmarshal(defaultCatalogueJSON, &a)
	json.Unmarshal(once, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the merge changed what the catalogue file says")
	}
	twice, _ := MergeMeasured(once)
	if !bytes.Equal(once, twice) {
		t.Fatal("a second merge changed the file")
	}
}
