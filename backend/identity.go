package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// 身份相关文件都放在用户配置目录的 NetDream/ 下：identity.json 装着本机私钥
// （0600），trusted.json 是持久信任列表，identities.json 是签发者身份缓存。
const (
	identityFileName    = "identity.json"
	trustedFileName     = "trusted.json"
	identitiesFileName  = "identities.json"
	identityFileVersion = 1
	// 身份缓存只留最近这么多条：它是加密保存的收件人列表，
	// 更老的历史身份只会让列表难找，还会无限涨。
	identityCacheLimit = 200
)

// identityMu 保护三类文件的读改写、进程内「信任一次」集合和身份缓存：
// 绑定方法可能被前端从不同 goroutine 同时调用。
var identityMu sync.Mutex

// trustedOnce 是「信任一次」的交接令牌：ResolveTrust(remember=false) 放入指纹，
// finishBandRead 取走它继续这次读盘。取走即失效——信任一次就是只对这一次打开生效，
// 下次打开同一个文件仍会问。
var trustedOnce = map[string]bool{}

// localIdentity 是本机身份的解码形态。私钥只在本进程里用，从不经绑定方法出去。
type localIdentity struct {
	ID          string
	Email       string
	Ed25519Priv ed25519.PrivateKey
	Ed25519Pub  ed25519.PublicKey
	ECDHPriv    *ecdh.PrivateKey
	ECDHPub     *ecdh.PublicKey
	CreatedAt   int64
}

// identityFile 是 identity.json 的落盘形态。
type identityFile struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Email       string `json:"email"`
	Ed25519Priv string `json:"ed25519Priv"`
	Ed25519Pub  string `json:"ed25519Pub"`
	ECDHPriv    string `json:"ecdhPriv"`
	ECDHPub     string `json:"ecdhPub"`
	CreatedAt   int64  `json:"createdAt"`
}

// cachedIdentity 是 identities.json 的行：比候选多一个 ed25519 公钥
// （将来按公钥核对签名要用），只在这份缓存里存。
type cachedIdentity struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
	ECDH        string `json:"ecdh"`
	Ed25519     string `json:"ed25519"`
}

// IdentityService 是本机身份与信任列表的入口。
// Every exported method here is bound to the frontend, so helpers stay unexported.
type IdentityService struct{}

// Get 返回当前身份；还没建立时 status 是 "unset"。
func (IdentityService) Get() IdentityInfo {
	local, err := loadLocalIdentity()
	if err != nil || local == nil {
		return IdentityInfo{Status: "unset"}
	}
	return local.info()
}

// Setup 首次运行建立身份：ed25519 签名密钥对 + X25519 密钥对。email 留空时
// 随机取「形容词的+名词」@主机名。已经建立过就原样返回，不重建密钥——
// 换密钥等于换一个身份，别人签给旧身份的信任和加密文件会全部失效。
func (IdentityService) Setup(email string) (IdentityInfo, error) {
	identityMu.Lock()
	defer identityMu.Unlock()

	if local, err := loadLocalIdentityLocked(); err != nil {
		return IdentityInfo{}, err
	} else if local != nil {
		return local.info(), nil
	}

	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return IdentityInfo{}, fmt.Errorf("生成本机签名密钥失败：%w", err)
	}
	ecdhPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return IdentityInfo{}, fmt.Errorf("生成本机加密密钥失败：%w", err)
	}

	email = strings.TrimSpace(email)
	if email == "" {
		email = randomDisplayName()
	}
	local := &localIdentity{
		// 首次创建没有单独的展示 ID，先用 email 顶上，Update 可改。
		ID:          email,
		Email:       email,
		Ed25519Priv: edPriv,
		Ed25519Pub:  edPub,
		ECDHPriv:    ecdhPriv,
		ECDHPub:     ecdhPriv.PublicKey(),
		CreatedAt:   time.Now().UnixMilli(),
	}
	if err := storeLocalIdentityLocked(local); err != nil {
		return IdentityInfo{}, err
	}
	return local.info(), nil
}

// Update 只改展示信息（email / id）：指纹与密钥不变，旧签名包的验证不受影响。
// 空串表示这一项不动。
func (IdentityService) Update(email, id string) (IdentityInfo, error) {
	identityMu.Lock()
	defer identityMu.Unlock()

	local, err := loadLocalIdentityLocked()
	if err != nil {
		return IdentityInfo{}, err
	}
	if local == nil {
		return IdentityInfo{}, fmt.Errorf("本机还没有身份：请先调用 Setup")
	}
	if trimmed := strings.TrimSpace(email); trimmed != "" {
		local.Email = trimmed
	}
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		local.ID = trimmed
	}
	if err := storeLocalIdentityLocked(local); err != nil {
		return IdentityInfo{}, err
	}
	return local.info(), nil
}

// Candidates 是加密保存时的收件人候选：自己、持久信任、身份缓存，按指纹去重。
func (IdentityService) Candidates() []IdentityCandidate {
	out := make([]IdentityCandidate, 0, 4)
	seen := map[string]bool{}

	local, err := loadLocalIdentity()
	if err == nil && local != nil {
		candidate := local.candidate()
		out = append(out, candidate)
		seen[candidate.Fingerprint] = true
	}
	for _, trusted := range readTrustedIdentities() {
		if seen[trusted.Fingerprint] {
			continue
		}
		seen[trusted.Fingerprint] = true
		out = append(out, IdentityCandidate{
			ID: trusted.ID, Email: trusted.Email,
			Fingerprint: trusted.Fingerprint, ECDH: trusted.ECDH,
		})
	}
	for _, cached := range readCachedIdentities() {
		if seen[cached.Fingerprint] {
			continue
		}
		seen[cached.Fingerprint] = true
		out = append(out, cached.candidate())
	}
	return out
}

// ImportFromBand 从一个 .band 的 header 里导入签发者公钥：加密的 band 也能导
// （头和身份恒为明文），但只读头、不信任、不解密。
func (IdentityService) ImportFromBand() (IdentityCandidate, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   "从工作区导入身份",
		Filters: []mygo.FileFilter{{Name: "NetDream 工作区", Extensions: []string{"band"}}},
	})
	if err != nil {
		return IdentityCandidate{}, fmt.Errorf("打开工作区对话框失败：%w", err)
	}
	if len(paths) == 0 || paths[0] == "" {
		return IdentityCandidate{}, nil
	}

	name := filepath.Base(paths[0])
	header, err := readBandHeader(paths[0], defaultBandContainerLimits())
	if err != nil {
		return IdentityCandidate{}, err
	}
	if header.Identity == nil {
		return IdentityCandidate{}, fmt.Errorf("%s 没有签发者身份：v1 归档或未建立身份时保存的文件不携带公钥", name)
	}
	// 指纹已经和公钥核对过（验签里做），缓存里存的是可信的一组公钥。
	if err := cacheIdentity(header.Identity.cached()); err != nil {
		return IdentityCandidate{}, err
	}
	return header.Identity.candidate(), nil
}

// Trusted 返回持久信任列表。
func (IdentityService) Trusted() []TrustedIdentity {
	return readTrustedIdentities()
}

// Untrust 从持久信任列表里移除一个指纹，同时清掉本次进程里的「信任一次」。
// 移除失败（文件写不了）时列表保持原样：下次读到的还是旧的一份。
func (IdentityService) Untrust(fingerprint string) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return
	}
	identityMu.Lock()
	defer identityMu.Unlock()

	forgetTrustOnceLocked(fingerprint)
	trusted, err := readTrustedIdentitiesLocked()
	if err != nil {
		return
	}
	kept := make([]TrustedIdentity, 0, len(trusted))
	for _, t := range trusted {
		if strings.ToLower(t.Fingerprint) != fingerprint {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(trusted) {
		return
	}
	_ = writeStoreFileLocked(trustedFileName, kept)
}

// identityStoreDir 是本机身份数据的目录（Windows 上是 %APPDATA%\NetDream）。
func identityStoreDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录：%w", err)
	}
	dir := filepath.Join(base, "NetDream")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建配置目录 %s 失败：%w", dir, err)
	}
	return dir, nil
}

// storePath 是身份数据文件在配置目录里的路径。
func storePath(name string) (string, error) {
	dir, err := identityStoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// writeStoreFileLocked 原子地写一份 0600 的配置文件：先写同目录临时文件再 rename，
// 半截写入不会把身份密钥或信任列表毁掉。调用方必须持有 identityMu。
func writeStoreFileLocked(name string, value any) error {
	path, err := storePath(name)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 %s 失败：%w", name, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+name+".*.tmp")
	if err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		// Windows 上开着的文件删不掉：先关后删。
		tmp.Close()
		if !done {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if err := renameStoreFile(tmpName, path); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	done = true
	return nil
}

// renameStoreFile 替换配置文件：Windows 上杀毒与索引服务会短暂占住刚写的文件，
// 一次 ACCESS_DENIED 就放弃太脆，隔一会儿重试几次即可。
func renameStoreFile(oldPath, newPath string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(oldPath, newPath); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

// readStoreFileLocked 读一份配置文件；文件不存在返回 (nil, nil)。
func readStoreFileLocked(name string) ([]byte, error) {
	path, err := storePath(name)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s 失败：%w", name, err)
	}
	return body, nil
}

// loadLocalIdentity 读本机身份；没建立过返回 (nil, nil)。
func loadLocalIdentity() (*localIdentity, error) {
	identityMu.Lock()
	defer identityMu.Unlock()
	return loadLocalIdentityLocked()
}

func loadLocalIdentityLocked() (*localIdentity, error) {
	body, err := readStoreFileLocked(identityFileName)
	if err != nil || body == nil {
		return nil, err
	}

	var stored identityFile
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, fmt.Errorf("%s 无法解析：%w", identityFileName, err)
	}
	edPriv, err := decodeKey(stored.Ed25519Priv, ed25519.PrivateKeySize)
	if err != nil {
		return nil, fmt.Errorf("%s 的签名私钥不合法：%w", identityFileName, err)
	}
	edPub, err := decodeKey(stored.Ed25519Pub, ed25519.PublicKeySize)
	if err != nil {
		return nil, fmt.Errorf("%s 的签名公钥不合法：%w", identityFileName, err)
	}
	// ed25519 私钥是 seed‖公钥：文件被手动改坏时签名会用一套、指纹显示另一套，
	// 不如当场拒绝。
	if !bytes.Equal(edPriv[ed25519.SeedSize:], edPub) {
		return nil, fmt.Errorf("%s 的签名密钥对不匹配", identityFileName)
	}
	ecdhPrivBody, err := decodeKey(stored.ECDHPriv, 32)
	if err != nil {
		return nil, fmt.Errorf("%s 的加密私钥不合法：%w", identityFileName, err)
	}
	ecdhPubBody, err := decodeKey(stored.ECDHPub, 32)
	if err != nil {
		return nil, fmt.Errorf("%s 的加密公钥不合法：%w", identityFileName, err)
	}
	ecdhPriv, err := ecdh.X25519().NewPrivateKey(ecdhPrivBody)
	if err != nil {
		return nil, fmt.Errorf("%s 的加密私钥不合法：%w", identityFileName, err)
	}
	ecdhPub, err := ecdh.X25519().NewPublicKey(ecdhPubBody)
	if err != nil {
		return nil, fmt.Errorf("%s 的加密公钥不合法：%w", identityFileName, err)
	}
	if !ecdhPriv.PublicKey().Equal(ecdhPub) {
		return nil, fmt.Errorf("%s 的加密密钥对不匹配", identityFileName)
	}

	return &localIdentity{
		ID:          stored.ID,
		Email:       stored.Email,
		Ed25519Priv: ed25519.PrivateKey(edPriv),
		Ed25519Pub:  ed25519.PublicKey(edPub),
		ECDHPriv:    ecdhPriv,
		ECDHPub:     ecdhPub,
		CreatedAt:   stored.CreatedAt,
	}, nil
}

// storeLocalIdentityLocked 把身份写进 identity.json。
func storeLocalIdentityLocked(local *localIdentity) error {
	stored := identityFile{
		Version:     identityFileVersion,
		ID:          local.ID,
		Email:       local.Email,
		Ed25519Priv: base64.StdEncoding.EncodeToString(local.Ed25519Priv),
		Ed25519Pub:  base64.StdEncoding.EncodeToString(local.Ed25519Pub),
		ECDHPriv:    base64.StdEncoding.EncodeToString(local.ECDHPriv.Bytes()),
		ECDHPub:     base64.StdEncoding.EncodeToString(local.ECDHPub.Bytes()),
		CreatedAt:   local.CreatedAt,
	}
	return writeStoreFileLocked(identityFileName, stored)
}

// decodeKey 解一个 base64 公钥/私钥并核对长度：长度不对的密钥用在签名或 ECDH
// 上只会得到难以诊断的错误，不如当场拒绝。
func decodeKey(text string, size int) ([]byte, error) {
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	if len(body) != size {
		return nil, fmt.Errorf("密钥长度是 %d 字节，应为 %d 字节", len(body), size)
	}
	return body, nil
}

// identityFingerprint 是信任锚：ed25519 公钥的 sha256 hex。id/email 只是可改的
// 展示信息，改它们不影响任何已有签名包的验证。
func identityFingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

func (local *localIdentity) fingerprint() string {
	return identityFingerprint(local.Ed25519Pub)
}

func (local *localIdentity) info() IdentityInfo {
	return IdentityInfo{
		Status:      "ready",
		ID:          local.ID,
		Email:       local.Email,
		Fingerprint: local.fingerprint(),
		Ed25519:     base64.StdEncoding.EncodeToString(local.Ed25519Pub),
		ECDH:        base64.StdEncoding.EncodeToString(local.ECDHPub.Bytes()),
		CreatedAt:   local.CreatedAt,
	}
}

func (local *localIdentity) bandIdentity() *bandIdentity {
	return &bandIdentity{
		ID:          local.ID,
		Email:       local.Email,
		Ed25519:     base64.StdEncoding.EncodeToString(local.Ed25519Pub),
		ECDH:        base64.StdEncoding.EncodeToString(local.ECDHPub.Bytes()),
		Fingerprint: local.fingerprint(),
	}
}

func (local *localIdentity) candidate() IdentityCandidate {
	return IdentityCandidate{
		ID:          local.ID,
		Email:       local.Email,
		Fingerprint: local.fingerprint(),
		ECDH:        base64.StdEncoding.EncodeToString(local.ECDHPub.Bytes()),
	}
}

// readTrustedIdentities 读持久信任列表；读不出来按空列表处理（信任列表坏掉
// 不该让打开工作区整体失败，代价只是再问一次）。
func readTrustedIdentities() []TrustedIdentity {
	identityMu.Lock()
	defer identityMu.Unlock()
	trusted, err := readTrustedIdentitiesLocked()
	if err != nil {
		return []TrustedIdentity{}
	}
	return trusted
}

func readTrustedIdentitiesLocked() ([]TrustedIdentity, error) {
	body, err := readStoreFileLocked(trustedFileName)
	if err != nil || body == nil {
		return []TrustedIdentity{}, err
	}
	var trusted []TrustedIdentity
	if err := json.Unmarshal(body, &trusted); err != nil {
		return []TrustedIdentity{}, fmt.Errorf("%s 无法解析：%w", trustedFileName, err)
	}
	if trusted == nil {
		trusted = []TrustedIdentity{}
	}
	return trusted, nil
}

// addTrustedIdentity 把一个身份追加进持久信任列表，按指纹去重（已有的不动，
// 免得覆盖掉当初的 AddedAt）。
func addTrustedIdentity(identity TrustedIdentity) error {
	identityMu.Lock()
	defer identityMu.Unlock()

	trusted, err := readTrustedIdentitiesLocked()
	if err != nil {
		return err
	}
	fingerprint := strings.ToLower(strings.TrimSpace(identity.Fingerprint))
	for _, existing := range trusted {
		if strings.ToLower(existing.Fingerprint) == fingerprint {
			return nil
		}
	}
	identity.Fingerprint = fingerprint
	if identity.AddedAt == 0 {
		identity.AddedAt = time.Now().UnixMilli()
	}
	return writeStoreFileLocked(trustedFileName, append(trusted, identity))
}

// trustedFingerprints 把持久信任列表转成按指纹查找的集合；调用方必须持有 identityMu。
func trustedFingerprints() map[string]bool {
	trusted, err := readTrustedIdentitiesLocked()
	if err != nil {
		return map[string]bool{}
	}
	set := make(map[string]bool, len(trusted))
	for _, one := range trusted {
		set[strings.ToLower(strings.TrimSpace(one.Fingerprint))] = true
	}
	return set
}

// isTrustedSigner 报告一个签名者是否已经获得信任：自己的身份、trusted.json 里
// 的公钥，或者「信任一次」留下的令牌（取走即失效）。
func isTrustedSigner(fingerprint string) bool {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return true // 没有签名者：v1 与未建立身份时保存的文件，不涉及信任
	}
	if ownIdentityFingerprint() == fingerprint {
		return true
	}

	identityMu.Lock()
	defer identityMu.Unlock()
	if trustedOnce[fingerprint] {
		delete(trustedOnce, fingerprint)
		return true
	}
	return trustedFingerprints()[fingerprint]
}

// isPersistentlyTrusted 只看持久信任（自己和 trusted.json），不含「信任一次」：
// 决定要不要弹信任窗时用它，否则刚信任一次的文件会绕过下一次询问。
func isPersistentlyTrusted(fingerprint string) bool {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		return true
	}
	if ownIdentityFingerprint() == fingerprint {
		return true
	}
	identityMu.Lock()
	defer identityMu.Unlock()
	return trustedFingerprints()[fingerprint]
}

// ownIdentityFingerprint 是本机身份的指纹；没有身份时为空串。
func ownIdentityFingerprint() string {
	local, err := loadLocalIdentity()
	if err != nil || local == nil {
		return ""
	}
	return local.fingerprint()
}

// rememberTrustOnce 记下「信任一次」：finishBandRead 取走它继续这次读盘。
func rememberTrustOnce(fingerprint string) {
	identityMu.Lock()
	trustedOnce[strings.ToLower(strings.TrimSpace(fingerprint))] = true
	identityMu.Unlock()
}

func forgetTrustOnceLocked(fingerprint string) {
	delete(trustedOnce, strings.ToLower(strings.TrimSpace(fingerprint)))
}

// readCachedIdentities 读签发者身份缓存；坏掉按空处理。
func readCachedIdentities() []cachedIdentity {
	identityMu.Lock()
	defer identityMu.Unlock()
	cached, err := readCachedIdentitiesLocked()
	if err != nil {
		return []cachedIdentity{}
	}
	return cached
}

func readCachedIdentitiesLocked() ([]cachedIdentity, error) {
	body, err := readStoreFileLocked(identitiesFileName)
	if err != nil || body == nil {
		return []cachedIdentity{}, err
	}
	var cached []cachedIdentity
	if err := json.Unmarshal(body, &cached); err != nil {
		return []cachedIdentity{}, fmt.Errorf("%s 无法解析：%w", identitiesFileName, err)
	}
	if cached == nil {
		cached = []cachedIdentity{}
	}
	return cached, nil
}

// cacheIdentity 把签发者身份记进缓存：按指纹去重（重新出现的移到末尾 = 最近），
// 超出上限时丢最旧的一条。
func cacheIdentity(identity cachedIdentity) error {
	fingerprint := strings.ToLower(strings.TrimSpace(identity.Fingerprint))
	if fingerprint == "" {
		return nil
	}
	identity.Fingerprint = fingerprint

	identityMu.Lock()
	defer identityMu.Unlock()

	cached, err := readCachedIdentitiesLocked()
	if err != nil {
		return err
	}
	kept := make([]cachedIdentity, 0, len(cached)+1)
	for _, existing := range cached {
		if strings.ToLower(existing.Fingerprint) != fingerprint {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, identity)
	if len(kept) > identityCacheLimit {
		kept = kept[len(kept)-identityCacheLimit:]
	}
	return writeStoreFileLocked(identitiesFileName, kept)
}

func (cached cachedIdentity) candidate() IdentityCandidate {
	return IdentityCandidate{
		ID:          cached.ID,
		Email:       cached.Email,
		Fingerprint: cached.Fingerprint,
		ECDH:        cached.ECDH,
	}
}

// 首次运行时随机拼一个「形容词+名词@主机名」当展示名：比一串十六进制好认，
// 也不像真名。
var (
	identityAdjectives = []string{
		"勇敢", "安静", "明亮", "温柔", "冷静", "好奇", "迅捷", "沉稳",
		"敏捷", "坦率", "严谨", "幽默", "坚定", "轻盈", "洒脱", "热心",
		"执着", "机敏", "从容", "朴素", "炽热", "悠远", "灵巧", "大胆",
		"耐心", "古怪", "谦逊", "爽朗", "澄澈", "细致", "飘逸", "真诚",
		"倔强", "淡然", "勤勉", "磊落",
	}
	identityNouns = []string{
		"旅人", "工匠", "船长", "信使", "灯塔", "溪流", "松林", "星辰",
		"书架", "渡口", "候鸟", "山雀", "猎犬", "松鼠", "苔藓", "陶匠",
		"钟表匠", "邮差", "牧人", "渔夫", "画师", "抄写员", "园丁", "矿工",
		"夜莺", "蒲公英", "竹筏", "石桥", "沙丘", "雪原", "火塘", "茶盏",
		"罗盘", "风铃", "竹简", "海图",
	}
)

func randomDisplayName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "NetDream"
	}
	return randomWord(identityAdjectives) + "的" + randomWord(identityNouns) + "@" + host
}

// randomWord 从词表里取一个：用 crypto/rand 取，省得给 math/rand 播种。
func randomWord(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}
	return words[n.Int64()]
}
