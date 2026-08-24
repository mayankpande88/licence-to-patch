// Command depdiff reports the contract-relevant differences between two versions
// of a Go module — the detection core of the "Licence to Patch" agent, as a CLI.
//
// Usage:
//
//	depdiff <module-path> <from-version> <to-version>
//
// Example:
//
//	depdiff github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.12.0 v0.13.0
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mayankpande88/licence-to-patch/internal/brief"
	"github.com/mayankpande88/licence-to-patch/internal/depdiff"
	"github.com/mayankpande88/licence-to-patch/internal/verdict"
)

type output struct {
	depdiff.Report
	Preliminary verdict.Verdict `json:"preliminary"`
	Markdown    string          `json:"markdown"`
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: depdiff <module-path> <from-version> <to-version>")
		os.Exit(2)
	}
	rep, err := depdiff.Diff(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "depdiff: %v\n", err)
		os.Exit(1)
	}
	v := verdict.Classify(rep)
	out, _ := json.MarshalIndent(output{Report: rep, Preliminary: v, Markdown: brief.Explain(rep, v)}, "", "  ")
	fmt.Println(string(out))
}
