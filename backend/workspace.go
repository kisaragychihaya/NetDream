package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// bandVersion is the only workspace format version this app reads and writes.
const bandVersion = 1

// What a .band archive holds.
const (
	bandMetaEntry   = "workspace.json"
	bandRequestsDir = "requests/"
	bandExt         = ".band"
)

// bandMeta is workspace.json: what the archive is, not what it holds.
type bandMeta struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// WorkspaceService reads and writes the .band workspaces the page keeps.
// Every exported method here is bound to the frontend, so helpers stay unexported.
type WorkspaceService struct{}

// Open asks for a .band file and returns the workspace in it, or a zero
// WorkspaceFile when the user cancels the dialog.
func (WorkspaceService) Open() (WorkspaceFile, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   "打开工作区",
		Filters: []mygo.FileFilter{{Name: "NetDream 工作区", Extensions: []string{"band"}}},
	})
	if err != nil {
		return WorkspaceFile{}, fmt.Errorf("opening the workspace dialog failed: %w", err)
	}
	if len(paths) == 0 || paths[0] == "" {
		return WorkspaceFile{}, nil
	}

	data, err := readBand(paths[0])
	if err != nil {
		return WorkspaceFile{}, err
	}
	return WorkspaceFile{Path: paths[0], Data: data}, nil
}

// OpenPath reads the .band workspace at path, which the page got from a
// dropped file rather than the dialog: the path is known, so Save writes the
// workspace back to it.
func (WorkspaceService) OpenPath(path string) (WorkspaceFile, error) {
	if strings.TrimSpace(path) == "" {
		return WorkspaceFile{}, fmt.Errorf("opening a dropped workspace needs a path")
	}
	if !strings.EqualFold(filepath.Ext(path), bandExt) {
		return WorkspaceFile{}, fmt.Errorf("%s is not a %s workspace", filepath.Base(path), bandExt)
	}

	data, err := readBand(path)
	if err != nil {
		return WorkspaceFile{}, err
	}
	return WorkspaceFile{Path: path, Data: data}, nil
}

// Save writes data over the workspace at path.
func (WorkspaceService) Save(path string, data WorkspaceData) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("saving a workspace needs a path: use SaveAs for one")
	}
	return writeBand(path, data)
}

// SaveAs asks where to write data, writes it and returns the path, or "" when
// the user cancels the dialog.
func (WorkspaceService) SaveAs(data WorkspaceData) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       "工作区另存为",
		DefaultPath: bandFileName(data.Name),
		Filters:     []mygo.FileFilter{{Name: "NetDream 工作区", Extensions: []string{"band"}}},
	})
	if err != nil {
		return "", fmt.Errorf("opening the save dialog failed: %w", err)
	}
	if path == "" {
		return "", nil
	}

	path = bandPath(path)
	if err := writeBand(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// readBand reads the workspace an archive holds.
func readBand(path string) (WorkspaceData, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("opening %s failed: %w", filepath.Base(path), err)
	}
	return readBandBytes(body, filepath.Base(path))
}

// readBandBytes parses the .band archive in body: name is what errors call it.
func readBandBytes(body []byte, name string) (WorkspaceData, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("opening %s failed: %w", name, err)
	}

	var (
		meta    bandMeta
		hasMeta bool
	)
	requests := make([]SavedRequest, 0, len(zr.File))
	for _, entry := range zr.File {
		switch {
		case entry.Name == bandMetaEntry:
			entryBody, err := readZipEntry(entry)
			if err != nil {
				return WorkspaceData{}, fmt.Errorf("reading %s of %s failed: %w", bandMetaEntry, name, err)
			}
			if err := json.Unmarshal(entryBody, &meta); err != nil {
				return WorkspaceData{}, fmt.Errorf("%s is not a NetDream workspace: %w", name, err)
			}
			hasMeta = true
		case isRequestEntry(entry.Name):
			entryBody, err := readZipEntry(entry)
			if err != nil {
				continue // one unreadable request does not sink the workspace
			}
			var req SavedRequest
			if err := json.Unmarshal(entryBody, &req); err != nil {
				continue
			}
			requests = append(requests, filled(req))
		}
	}

	if !hasMeta {
		return WorkspaceData{}, fmt.Errorf("%s is not a NetDream workspace: it has no %s", name, bandMetaEntry)
	}
	if meta.Version != bandVersion {
		return WorkspaceData{}, fmt.Errorf("%s is a workspace of version %d, but only version %d is supported", name, meta.Version, bandVersion)
	}
	return WorkspaceData{Name: meta.Name, Requests: requests}, nil
}

// writeBand writes data to path as a .band archive, replacing what is there
// only once the whole archive is on disk.
func writeBand(path string, data WorkspaceData) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	meta, err := json.MarshalIndent(bandMeta{Version: bandVersion, Name: data.Name}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the workspace failed: %w", err)
	}
	if err := addZipEntry(zw, bandMetaEntry, meta); err != nil {
		return err
	}

	used := make(map[string]bool, len(data.Requests))
	for _, req := range data.Requests {
		body, err := json.MarshalIndent(req, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding request %q failed: %w", req.Name, err)
		}
		name := bandRequestsDir + requestEntryID(req.ID, used) + ".json"
		if err := addZipEntry(zw, name, body); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("writing the workspace archive failed: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s failed: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replacing %s failed: %w", filepath.Base(path), err)
	}
	return nil
}

// isRequestEntry reports whether an archive entry holds a saved request; every
// other entry is ignored.
func isRequestEntry(name string) bool {
	return strings.HasPrefix(name, bandRequestsDir) &&
		strings.HasSuffix(name, ".json") &&
		!strings.HasSuffix(name, "/")
}

// readZipEntry reads one entry of an archive.
func readZipEntry(entry *zip.File) ([]byte, error) {
	r, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// addZipEntry writes one entry of an archive.
func addZipEntry(zw *zip.Writer, name string, body []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("adding %s to the workspace failed: %w", name, err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("adding %s to the workspace failed: %w", name, err)
	}
	return nil
}

// requestEntryID names one request's entry: its id with what a file name can't
// carry replaced, and a count appended when that name is taken.
func requestEntryID(id string, used map[string]bool) string {
	name := bandID(id)
	if name == "" {
		name = "request"
	}
	unique := name
	for n := 2; used[unique]; n++ {
		unique = fmt.Sprintf("%s_%d", name, n)
	}
	used[unique] = true
	return unique
}

// bandID keeps the characters a file name may carry and replaces the rest.
func bandID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// bandFileName names the file to suggest for a workspace: its name with what a
// file name can't carry replaced, and the archive's extension.
func bandFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r), r < 0x20, r == 0x7f:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String() + bandExt
}

// bandPath gives path the archive's extension unless it already has it.
func bandPath(path string) string {
	if strings.HasSuffix(strings.ToLower(path), bandExt) {
		return path
	}
	return path + bandExt
}

// filled replaces absent lists with empty ones, so the page reads a list where
// it expects one and never null.
func filled(req SavedRequest) SavedRequest {
	if req.Params == nil {
		req.Params = []KeyValue{}
	}
	if req.Headers == nil {
		req.Headers = []KeyValue{}
	}
	if req.FormData == nil {
		req.FormData = []FormField{}
	}
	if req.URLEncoded == nil {
		req.URLEncoded = []KeyValue{}
	}
	if req.Cookies == nil {
		req.Cookies = []KeyValue{}
	}
	if req.Assertions == nil {
		req.Assertions = []SavedAssertion{}
	}
	if req.Messages == nil {
		req.Messages = []SavedMessage{}
	}
	return req
}
