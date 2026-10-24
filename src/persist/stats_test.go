package persist

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestTheSizesInAMongoReplyAreReadWhateverTheirNumberType(t *testing.T) {
	st := statsFromMongo(bson.M{
		"dataSize":    float64(1048576),
		"indexSize":   int32(2048),
		"storageSize": int64(4096),
		"fsUsedSize":  int64(10 << 30),
		"fsTotalSize": float64(20 << 30),
		"ignored":     "text",
	})
	if st.Backend != "mongodb" || st.DataBytes != 1048576 || st.IndexBytes != 2048 || st.StorageBytes != 4096 || st.DiskUsed != 10<<30 || st.DiskTotal != 20<<30 {
		t.Errorf("stats: %+v", st)
	}
	// a server that does not report its disk (Atlas free tiers) leaves those at 0
	if st := statsFromMongo(bson.M{"dataSize": int32(5)}); st.DiskTotal != 0 || st.DiskUsed != 0 || st.DataBytes != 5 {
		t.Errorf("no disk: %+v", st)
	}
	if number("12") != 0 || number(nil) != 0 {
		t.Error("something that is not a number counts as 0")
	}
}
