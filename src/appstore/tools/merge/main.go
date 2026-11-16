// merge puts the measurements of each CPU type (the files that `scripts/measure-catalogue.sh` writes) into appstore/catalogue_default.json.
//
//	go run appstore/tools/merge -catalogue appstore/catalogue_default.json measure-linux-64.json measure-linux-aarch64.json
package main

import (
	"appstore"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	cat := flag.String("catalogue", "appstore/catalogue_default.json", "the catalogue file to update")
	flag.Parse()
	base, err := os.ReadFile(*cat)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var parts [][]appstore.Entry
	for _, p := range flag.Args() {
		raw, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var es []appstore.Entry
		if err := json.Unmarshal(raw, &es); err != nil {
			fmt.Fprintln(os.Stderr, p+":", err)
			os.Exit(1)
		}
		parts = append(parts, es)
	}
	out, err := appstore.MergeMeasured(base, parts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*cat, out, 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var all []appstore.Entry
	for _, p := range parts {
		all = append(all, p...)
	}
	fmt.Println("options that could not be measured (kept out of the file):")
	for _, f := range appstore.MeasureFailures(parts...) {
		fmt.Println("  ", f)
	}
	fmt.Println("entries that need the owner's decision (the smallest option still has a graphics library):", appstore.NeedsDecision(all))
}
