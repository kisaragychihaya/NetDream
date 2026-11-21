package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
)

// bandVersion is the only workspace format version this app reads and writes.
const bandVersion = 1

// What a .band archive holds.
const (
	bandMetaEntry   = "workspace.json"
	bandVarsEntry   = "engine/vars.json"
	bandRequestsDir = "requests/"
	bandExt         = ".band"
)

// A .band is untrusted input: nothing stops a hand-made archive from carrying a
// file that unpacks to gigabytes. The limits below leave room for the largest
// thing this app writes (a 10 MiB response body, a Markdown document) with room
// to spare, and are enforced on what the archive actually unpacks to, not on the
// sizes it claims.
const (
	bandMaxArchiveSize = 128 << 20 // the .band file itself
	bandMaxEntrySize   = 32 << 20  // workspace.json, vars.json, or one request
	bandMaxTotalSize   = 256 << 20 // everything the archive unpacks to
)

// errBandLimit marks an archive over those limits. Unlike one unreadable
// request, an over-limit archive fails as a whole: silently skipping what is
// too big would drop data the user believes is saved.
var errBandLimit = errors.New("the workspace archive is over its size limits")

// bandMeta is workspace.json: what the archive is, not what it holds.
type bandMeta struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	// Folders 是请求树的分组；旧归档没有这个字段，读出来是 nil（= 全部请求在根）。
	Folders []SavedFolder `json:"folders"`
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

// SetDirty tells the window whether the workspace holds unsaved changes: while
// it does, closing the window asks the page before going ahead.
func (WorkspaceService) SetDirty(dirty bool) {
	guard.setDirty(dirty)
}

// CloseWindow closes the window even though the workspace may hold unsaved
// changes, once the page's user chose to discard them.
func (WorkspaceService) CloseWindow() {
	guard.close()
}

// readBand reads the workspace an archive holds.
func readBand(path string) (WorkspaceData, error) {
	name := filepath.Base(path)
	// Stat before ReadFile: a hand-made .band can be larger than the memory
	// this app is willing to spend on opening it.
	info, err := os.Stat(path)
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("opening %s failed: %w", name, err)
	}
	if info.Size() > bandMaxArchiveSize {
		return WorkspaceData{}, fmt.Errorf("%w: %s is %d MiB, the limit is %d MiB", errBandLimit, name, info.Size()>>20, bandMaxArchiveSize>>20)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("opening %s failed: %w", name, err)
	}
	return readBandBytes(body, name)
}

// bandBudget reads archive entries while holding the cumulative limit: every
// entry is capped, and so is what all of them add up to. name is what its errors
// call the archive; the limits are fields so a test can use small ones.
type bandBudget struct {
	name     string
	maxEntry int64
	maxTotal int64
	total    int64
}

// newBandBudget starts a read against the real limits.
func newBandBudget(name string) *bandBudget {
	return &bandBudget{name: name, maxEntry: bandMaxEntrySize, maxTotal: bandMaxTotalSize}
}

// read unpacks one entry. A breach of either limit reports errBandLimit, which
// readBandBytes treats as fatal rather than as a broken entry.
func (b *bandBudget) read(entry *zip.File) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(b.maxEntry) {
		return nil, fmt.Errorf("%s: %w: %s unpacks to %d MiB, one entry may not exceed %d MiB",
			b.name, errBandLimit, entry.Name, entry.UncompressedSize64>>20, b.maxEntry>>20)
	}
	r, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	// The declared size above is what the archive claims; read one byte past the
	// limit to find out what it really is, since a hand-made header can say
	// anything.
	body, err := io.ReadAll(io.LimitReader(r, b.maxEntry+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > b.maxEntry {
		return nil, fmt.Errorf("%s: %w: %s unpacks to more than %d MiB",
			b.name, errBandLimit, entry.Name, b.maxEntry>>20)
	}
	b.total += int64(len(body))
	if b.total > b.maxTotal {
		return nil, fmt.Errorf("%s: %w: its entries unpack to more than %d MiB in total",
			b.name, errBandLimit, b.maxTotal>>20)
	}
	return body, nil
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
		vars    map[string]string
	)
	requests := make([]SavedRequest, 0, len(zr.File))
	budget := newBandBudget(name)
	for _, entry := range zr.File {
		switch {
		case entry.Name == bandMetaEntry:
			entryBody, err := budget.read(entry)
			if err != nil {
				if errors.Is(err, errBandLimit) {
					return WorkspaceData{}, err
				}
				return WorkspaceData{}, fmt.Errorf("reading %s of %s failed: %w", bandMetaEntry, name, err)
			}
			if err := json.Unmarshal(entryBody, &meta); err != nil {
				return WorkspaceData{}, fmt.Errorf("%s is not a NetDream workspace: %w", name, err)
			}
			hasMeta = true
		case entry.Name == bandVarsEntry:
			// A vars.json that does not parse leaves the workspace with no
			// variables, like an unreadable request is skipped. An archive over
			// its limits is another matter: see the error below.
			entryBody, err := budget.read(entry)
			if err != nil {
				if errors.Is(err, errBandLimit) {
					return WorkspaceData{}, err
				}
				continue
			}
			var stored map[string]string
			if err := json.Unmarshal(entryBody, &stored); err != nil {
				continue
			}
			vars = stored
		case isRequestEntry(entry.Name):
			entryBody, err := budget.read(entry)
			if err != nil {
				if errors.Is(err, errBandLimit) {
					return WorkspaceData{}, err
				}
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
	if vars == nil {
		vars = map[string]string{} // the page reads a map where it expects one and never null
	}
	return WorkspaceData{
		Name:     meta.Name,
		Requests: requests,
		Vars:     vars,
		Folders:  normalizeFolders(meta.Folders, requests),
	}, nil
}

// normalizeFolders 把 workspace.json 的 folders 整理成页面可以直接信任的树：
// 字段缺失 → 空切片；每个文件夹的 requests 缺失 → 空切片；指向不存在请求的 ID
// 剔除（同一请求出现在多个文件夹时只有第一次出现生效）；parentId 指向不存在的
// 文件夹、或父子成环的文件夹提到根目录。坏数据的文件夹宁可丢，也不让页面树
// 画不出来或渲染空行。
func normalizeFolders(folders []SavedFolder, requests []SavedRequest) []SavedFolder {
	if len(folders) == 0 {
		return []SavedFolder{}
	}

	known := make(map[string]bool, len(requests))
	for _, req := range requests {
		known[req.ID] = true
	}

	kept := make([]SavedFolder, 0, len(folders))
	placed := make(map[string]bool, len(folders)) // 已经归属到某个文件夹的请求
	ids := make(map[string]bool, len(folders))    // 已收下的文件夹 id
	for _, folder := range folders {
		// 空 id 或重复 id 的文件夹引用不出唯一的节点，直接丢。
		if folder.ID == "" || ids[folder.ID] {
			continue
		}
		ids[folder.ID] = true
		kept = append(kept, SavedFolder{
			ID:       folder.ID,
			Name:     folder.Name,
			ParentID: folder.ParentID,
			Requests: normalizeFolderRequests(folder.Requests, known, placed),
		})
	}

	parents := make(map[string]string, len(kept))
	for i := range kept {
		// 悬空的 parentId：提回根目录，否则整个子树在页面里没有落脚点。
		if kept[i].ParentID != "" && !ids[kept[i].ParentID] {
			kept[i].ParentID = ""
		}
		parents[kept[i].ID] = kept[i].ParentID
	}
	// 成环的父子链（A 的父是 B、B 的父是 A）从根走不到，把环上每个成员都提回根。
	for i := range kept {
		seen := make(map[string]bool, len(kept))
		for parent := kept[i].ParentID; parent != ""; parent = parents[parent] {
			if parent == kept[i].ID || seen[parent] {
				kept[i].ParentID = ""
				break
			}
			seen[parent] = true
		}
	}
	return kept
}

// normalizeFolderRequests 留下文件夹里真实存在、且还没被别的文件夹领走的请求 ID，
// 保持原有顺序。
func normalizeFolderRequests(ids []string, known, placed map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !known[id] || placed[id] {
			continue
		}
		placed[id] = true
		out = append(out, id)
	}
	return out
}

// bandWriteMu 串行化本进程里的工作区写盘：同一路径的两次保存否则会在临时文件
// 和 rename 上互相踩（Windows 上并发替换同一目标会直接 EBUSY/拒绝访问）。
// 保存是低频的用户动作，一把锁比按路径维护锁表划算。
var bandWriteMu sync.Mutex

// writeBand writes data to path as a .band archive, replacing what is there
// only once the whole archive is on disk.
func writeBand(path string, data WorkspaceData) error {
	bandWriteMu.Lock()
	defer bandWriteMu.Unlock()

	// 临时文件与目标同目录、名字唯一：同一路径的两次保存在时间上重叠也不会互相
	// 踩掉（原来共用 path+".tmp"）。写完再 rename，替换仍然是原子的。
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s failed: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		// 失败路径上文件可能还开着：Windows 上开着的文件删不掉，先关再删。
		tmp.Close()
		if !done {
			os.Remove(tmpName) // 替换成功之前失败：别留下半截临时文件
		}
	}()

	// 归档直接流进临时文件，不再先在内存里攒出一整份：响应示例每条能带 10MiB
	// 的响应体，几百条请求的工作区攒成一块就会把进程内存顶上去。
	zw := zip.NewWriter(tmp)
	if err := writeBandEntries(zw, data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("writing the workspace archive failed: %w", err)
	}
	// os.CreateTemp 建的是 0600，写出来的归档保持原来直接写的可读权限。
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("writing %s failed: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s failed: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s failed: %w", filepath.Base(path), err)
	}
	done = true
	return nil
}

// writeBandEntries encodes the workspace into zw: metadata, variables, then one
// entry per request.
func writeBandEntries(zw *zip.Writer, data WorkspaceData) error {
	// folders 总是写（空也写）：旧归档里留着上一份树的话，页面清空分组后
	// 那些文件夹会再冒出来。
	folders := data.Folders
	if folders == nil {
		folders = []SavedFolder{}
	}
	meta, err := json.MarshalIndent(bandMeta{Version: bandVersion, Name: data.Name, Folders: folders}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the workspace failed: %w", err)
	}
	if err := addZipEntry(zw, bandMetaEntry, meta); err != nil {
		return err
	}

	// Written every time, empty or not: an older archive's variables would
	// otherwise stay behind after the page cleared them.
	vars := data.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	varsBody, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the workspace variables failed: %w", err)
	}
	if err := addZipEntry(zw, bandVarsEntry, varsBody); err != nil {
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
	return nil
}

// isRequestEntry reports whether an archive entry holds a saved request; every
// other entry is ignored.
func isRequestEntry(name string) bool {
	return strings.HasPrefix(name, bandRequestsDir) &&
		strings.HasSuffix(name, ".json") &&
		!strings.HasSuffix(name, "/")
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
	if req.SocketEvents == nil {
		req.SocketEvents = []SavedSocketEvent{}
	}
	if req.Examples == nil {
		req.Examples = []SavedExample{}
	}
	return req
}
