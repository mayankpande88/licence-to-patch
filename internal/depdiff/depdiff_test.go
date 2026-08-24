package depdiff

import (
	"os"
	"path/filepath"
	"testing"
)

// writeModule lays down a throwaway module dir with the given files.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The api-version literal lives in a typed const in one file and is referenced
// from a client file — mirroring how the Azure SDK for Go generates clients.
const constsOld = `package armmonitor

const (
	version20240301Preview string = "2024-03-01-preview"
)
`

const constsNew = `package armmonitor

const (
	version20260101 string = "2026-01-01"
)
`

const clientOld = `package armmonitor

func (c *MetricAlertsClient) req() {
	reqQP.Set("api-version", version20240301Preview)
}
`

const clientNew = `package armmonitor

func (c *MetricAlertsClient) req() {
	reqQP.Set("api-version", version20260101)
}
`

func TestDiffAPIVersions_DetectsContractChange(t *testing.T) {
	from := writeModule(t, map[string]string{
		"constants.go":           constsOld,
		"metricalerts_client.go": clientOld,
	})
	to := writeModule(t, map[string]string{
		"constants.go":           constsNew,
		"metricalerts_client.go": clientNew,
	})

	changes := diffAPIVersions(from, to)
	if len(changes) != 1 {
		t.Fatalf("expected 1 api-version change, got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.File != "metricalerts_client.go" || c.From != "2024-03-01-preview" || c.To != "2026-01-01" {
		t.Fatalf("unexpected change: %+v", c)
	}
}

func TestDiffAPIVersions_NoChangeWhenStable(t *testing.T) {
	from := writeModule(t, map[string]string{"constants.go": constsOld, "c.go": clientOld})
	to := writeModule(t, map[string]string{"constants.go": constsOld, "c.go": clientOld})
	if changes := diffAPIVersions(from, to); len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestApiVersionByFile_InlineLiteral(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"c.go": "package p\nfunc f(){ reqQP.Set(\"api-version\", \"2021-05-01\") }\n",
	})
	got := apiVersionByFile(dir)
	if got["c.go"] != "2021-05-01" {
		t.Fatalf("expected inline literal 2021-05-01, got %q", got["c.go"])
	}
}
