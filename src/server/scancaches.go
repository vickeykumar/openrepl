package server

import (
	"time"

	"utils"
)

// Lists the dashboard and the public pages read by scanning a whole database collection. On
// Firestore a scan costs a read for every record, and the admin views ask every few seconds, so each
// list is kept (utils.ScanCache): five minutes, or ten for the blog, dropped when this server
// changes the data, and read again on request (Refresh) at most once in half a minute. The cache
// of the accounts is in the user package, and the Genie usage records are in tokens.go.
var (
	feedbackScan = &utils.ScanCache[map[int64]feedback]{TTL: 5 * time.Minute, MinInterval: 30 * time.Second}
	snippetScan  = &utils.ScanCache[[]snippetRow]{TTL: 5 * time.Minute, MinInterval: 30 * time.Second}
	// a post an admin saves shows at once; a visitor's page is then a few seconds behind at most
	blogScan = &utils.ScanCache[map[string]BlogPost]{TTL: 10 * time.Minute, MinInterval: 5 * time.Second}
)

// resetScanCaches forgets every list. A database handle that was just opened has a list of its
// own, and tests open many.
func resetScanCaches() {
	feedbackScan.Reset()
	snippetScan.Reset()
	blogScan.Reset()
}

// wantFresh is true when the dashboard asked for a new scan (the Refresh button): ?fresh=1.
func wantFresh(query interface{ Get(string) string }) bool { return query.Get("fresh") == "1" }
