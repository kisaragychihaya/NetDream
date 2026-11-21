package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 一个解压后超过单条上限的条目：整个归档必须报错，而不是安静地把它当坏请求跳过。
func TestReadBandRejectsOversizedEntry(t *testing.T) {
	body := zipArchive(t, []zipEntry{
		{name: bandMetaEntry, body: `{"version":1,"name":"炸弹"}`},
		{name: bandRequestsDir + "big.json", body: strings.Repeat("a", bandMaxEntrySize+1)},
	})
	_, err := readBandBytes(body, "big.band")
	if !errors.Is(err, errBandLimit) {
		t.Fatalf("readBandBytes error = %v, want errBandLimit", err)
	}
	if !strings.Contains(err.Error(), "big.json") {
		t.Fatalf("error %q does not name the oversized entry", err)
	}
}

// 单条超限的判定发生在解压之前：声明的大小就够看出不合法。
func TestBandBudgetRejectsOversizedEntryByDeclaredSize(t *testing.T) {
	zr := zipOf(t, []zipEntry{{name: "big.json", body: strings.Repeat("a", 2000)}})
	budget := &bandBudget{name: "small.band", maxEntry: 1000, maxTotal: 1 << 20}
	_, err := budget.read(zr.File[0])
	if !errors.Is(err, errBandLimit) {
		t.Fatalf("bandBudget.read error = %v, want errBandLimit", err)
	}
	if !strings.Contains(err.Error(), "unpacks to") || !strings.Contains(err.Error(), "big.json") {
		t.Fatalf("error %q does not explain the per-entry limit", err)
	}
}

// 每条都在单条上限内、但加起来超过累计上限：同样必须报错。
func TestBandBudgetRejectsTotalOverLimit(t *testing.T) {
	zr := zipOf(t, []zipEntry{
		{name: "a.json", body: strings.Repeat("a", 800)},
		{name: "b.json", body: strings.Repeat("b", 800)},
	})
	budget := &bandBudget{name: "small.band", maxEntry: 1000, maxTotal: 1500}
	if _, err := budget.read(zr.File[0]); err != nil {
		t.Fatalf("first entry failed: %v", err)
	}
	_, err := budget.read(zr.File[1])
	if !errors.Is(err, errBandLimit) {
		t.Fatalf("bandBudget.read error = %v, want errBandLimit", err)
	}
	if !strings.Contains(err.Error(), "in total") {
		t.Fatalf("error %q does not explain the cumulative limit", err)
	}
}

// 限额不能误伤正常内容：后端允许的最大响应体（10MiB）照样读得出来。
func TestReadBandAcceptsLargeResponse(t *testing.T) {
	big := `{"id":"r1","name":"大响应","kind":"http","url":"https://example.com","response":{"body":"` +
		strings.Repeat("a", 10<<20) + `"}}`
	body := zipArchive(t, []zipEntry{
		{name: bandMetaEntry, body: `{"version":1,"name":"大响应"}`},
		{name: bandRequestsDir + "r1.json", body: big},
	})
	got, err := readBandBytes(body, "big.band")
	if err != nil {
		t.Fatalf("readBandBytes failed: %v", err)
	}
	if len(got.Requests) != 1 || got.Requests[0].ID != "r1" {
		t.Fatalf("requests = %#v, want the 10MiB request", got.Requests)
	}
}

// 文件本身超过归档上限时，连读都不读：先看大小。
func TestReadBandRejectsOversizedArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.band")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s failed: %v", path, err)
	}
	// 只扩展长度、不写内容：这条断言只关心大小，读不起来也无所谓。
	if err := file.Truncate(bandMaxArchiveSize + 1); err != nil {
		file.Close()
		t.Fatalf("growing %s failed: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("closing %s failed: %v", path, err)
	}

	_, err = readBand(path)
	if !errors.Is(err, errBandLimit) {
		t.Fatalf("readBand error = %v, want errBandLimit", err)
	}
	if !strings.Contains(err.Error(), "huge.band") {
		t.Fatalf("error %q does not name the archive", err)
	}
}

// zipOf 把归档读成 zip.File 列表，供 bandBudget 的直接测试使用。
func zipOf(t *testing.T, entries []zipEntry) *zip.Reader {
	t.Helper()
	body := zipArchive(t, entries)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("opening the test archive failed: %v", err)
	}
	return zr
}
