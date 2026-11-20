package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testRequest 造一条带内容的请求：往返测试要确认请求内容没被 folders 改动。
func testRequest(id, name string) SavedRequest {
	return filled(SavedRequest{
		ID:      id,
		Name:    name,
		Kind:    "http",
		Method:  "GET",
		URL:     "https://example.com/" + id,
		Params:  []KeyValue{{Key: "a", Value: "1"}},
		Headers: []KeyValue{{Key: "X-Test", Value: id}},
	})
}

// zipArchive 按给定顺序拼一个 .band 归档（手工造的旧格式/坏数据用）。
func zipArchive(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("creating %s failed: %v", entry.name, err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatalf("writing %s failed: %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the archive failed: %v", err)
	}
	return buf.Bytes()
}

type zipEntry struct {
	name string
	body string
}

// archiveWith 造一个 workspace.json 为给定 meta、requests/ 下逐条请求的归档。
func archiveWith(t *testing.T, meta string, requests ...SavedRequest) []byte {
	t.Helper()
	entries := []zipEntry{{name: bandMetaEntry, body: meta}}
	for _, req := range requests {
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("encoding request %q failed: %v", req.ID, err)
		}
		entries = append(entries, zipEntry{name: bandRequestsDir + req.ID + ".json", body: string(body)})
	}
	return zipArchive(t, entries)
}

// archiveEntry 读回归档里某个条目的原文。
func archiveEntry(t *testing.T, body []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("opening the archive failed: %v", err)
	}
	for _, entry := range zr.File {
		if entry.Name != name {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatalf("opening %s failed: %v", entry.Name, err)
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("reading %s failed: %v", entry.Name, err)
		}
		return string(got)
	}
	t.Fatalf("the archive has no %s", name)
	return ""
}

// folders 写读往返：嵌套的 parentId、文件夹里的请求顺序都要原样回来。
func TestWorkspaceFoldersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.band")
	want := WorkspaceData{
		Name: "分组工作区",
		Requests: []SavedRequest{
			testRequest("r1", "第一条"),
			testRequest("r2", "第二条"),
			testRequest("r3", "根目录的一条"),
		},
		Vars: map[string]string{"host": "localhost", "token": "abc"},
		Folders: []SavedFolder{
			{ID: "f1", Name: "集合", Requests: []string{"r2", "r1"}},
			{ID: "f2", Name: "子集合", ParentID: "f1", Requests: []string{}},
			{ID: "f3", Name: "另一个根文件夹", Requests: []string{}},
		},
	}
	if err := writeBand(path, want); err != nil {
		t.Fatalf("writeBand failed: %v", err)
	}

	got, err := readBand(path)
	if err != nil {
		t.Fatalf("readBand failed: %v", err)
	}
	if !reflect.DeepEqual(got.Folders, want.Folders) {
		t.Fatalf("folders after a round trip = %#v, want %#v", got.Folders, want.Folders)
	}
	// 不回归：请求内容与变量照旧。
	if !reflect.DeepEqual(got.Requests, want.Requests) {
		t.Fatalf("requests after a round trip changed: %#v", got.Requests)
	}
	if !reflect.DeepEqual(got.Vars, want.Vars) {
		t.Fatalf("vars after a round trip = %#v, want %#v", got.Vars, want.Vars)
	}
	if got.Name != want.Name {
		t.Fatalf("name after a round trip = %q, want %q", got.Name, want.Name)
	}
}

// 空 folders 也要落盘成 []：否则旧归档里的树会留在原地。
func TestWorkspaceWritesEmptyFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.band")
	if err := writeBand(path, WorkspaceData{Name: "空", Requests: []SavedRequest{testRequest("r1", "一")}}); err != nil {
		t.Fatalf("writeBand failed: %v", err)
	}
	body := readFileBytes(t, path)
	if meta := archiveEntry(t, body, bandMetaEntry); !strings.Contains(meta, `"folders": []`) {
		t.Fatalf("workspace.json = %s, want an empty folders list", meta)
	}

	got, err := readBandBytes(body, "empty.band")
	if err != nil {
		t.Fatalf("readBandBytes failed: %v", err)
	}
	if got.Folders == nil || len(got.Folders) != 0 {
		t.Fatalf("folders of an archive without folders = %#v, want an empty list", got.Folders)
	}
}

// 旧格式：workspace.json 没有 folders 字段 → 空结构，请求全在根目录。
func TestWorkspaceReadsLegacyArchive(t *testing.T) {
	body := archiveWith(t,
		`{"version":1,"name":"旧工作区"}`,
		testRequest("r1", "一"),
		testRequest("r2", "二"),
	)
	got, err := readBandBytes(body, "legacy.band")
	if err != nil {
		t.Fatalf("readBandBytes failed: %v", err)
	}
	if got.Folders == nil || len(got.Folders) != 0 {
		t.Fatalf("folders of a legacy archive = %#v, want an empty list", got.Folders)
	}
	if len(got.Requests) != 2 {
		t.Fatalf("requests of a legacy archive = %d, want 2", len(got.Requests))
	}
	if got.Requests[0].ID != "r1" || got.Requests[1].ID != "r2" {
		t.Fatalf("request order changed: %s, %s", got.Requests[0].ID, got.Requests[1].ID)
	}
	// 旧的归档没有 vars.json：变量表是空表而不是 nil。
	if got.Vars == nil || len(got.Vars) != 0 {
		t.Fatalf("vars of a legacy archive = %#v, want an empty map", got.Vars)
	}
}

// 归一化：不存在的请求剔除、同一请求只归第一个文件夹、悬空 parentId 提根。
func TestWorkspaceNormalizesFolders(t *testing.T) {
	body := archiveWith(t, `{"version":1,"name":"坏树","folders":[
		{"id":"f1","name":"有孤儿","parentId":"","requests":["r1","gone","r2"]},
		{"id":"f2","name":"抢重复的","parentId":"","requests":["r2","r3"]},
		{"id":"f3","name":"父不存在","parentId":"nope","requests":[]},
		{"id":"f4","name":"环的一边","parentId":"f5","requests":[]},
		{"id":"f5","name":"环的另一边","parentId":"f4","requests":[]},
		{"id":"f1","name":"重复的 id","parentId":"","requests":[]}
	]}`,
		testRequest("r1", "一"),
		testRequest("r2", "二"),
		testRequest("r3", "三"),
	)
	got, err := readBandBytes(body, "broken.band")
	if err != nil {
		t.Fatalf("readBandBytes failed: %v", err)
	}

	want := []SavedFolder{
		{ID: "f1", Name: "有孤儿", Requests: []string{"r1", "r2"}},
		{ID: "f2", Name: "抢重复的", Requests: []string{"r3"}},
		{ID: "f3", Name: "父不存在", Requests: []string{}},
		{ID: "f4", Name: "环的一边", Requests: []string{}},
		{ID: "f5", Name: "环的另一边", Requests: []string{}},
	}
	if !reflect.DeepEqual(got.Folders, want) {
		t.Fatalf("normalized folders = %#v, want %#v", got.Folders, want)
	}
}

// 单个文件夹的 requests 缺失 → 空切片（页面读的是数组，不能是 null）。
func TestWorkspaceFillsFolderRequests(t *testing.T) {
	body := archiveWith(t,
		`{"version":1,"name":"缺 requests","folders":[{"id":"f1","name":"空文件夹","parentId":""}]}`,
		testRequest("r1", "一"),
	)
	got, err := readBandBytes(body, "missing.band")
	if err != nil {
		t.Fatalf("readBandBytes failed: %v", err)
	}
	if len(got.Folders) != 1 {
		t.Fatalf("folders = %#v, want one folder", got.Folders)
	}
	if got.Folders[0].Requests == nil || len(got.Folders[0].Requests) != 0 {
		t.Fatalf("requests of the folder = %#v, want an empty list", got.Folders[0].Requests)
	}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	return body
}
