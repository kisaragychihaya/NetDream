package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// v2 容器：| "NDB2" | u32le 头长 | header JSON（明文） | zip 区域 |。
// 压缩先于加密：zip 先 deflate，密文不经过压缩器，压缩率无损。
const (
	bandMagicV2          = "NDB2"
	bandFormatV2         = "ndband"
	bandContainerVersion = 2

	bandContainerV1 = 1
	bandContainerV2 = 2
)

// v2 的限额，判定一律按明文（加密不改变「能装多少内容」）。头是明文，
// 读它之前没法相信它自己说的大小，所以头另有一条独立上限。
const (
	bandMaxHeaderSize = 1 << 20   // header JSON
	bandMaxRegionSize = 128 << 20 // zip 区域明文
	bandChunkSize     = 4 << 20   // 分块 AEAD 的块大小
	bandMaxChunkSize  = 16 << 20  // 读侧对头里 chunkSize 的 sanity cap
	// 每个密文块比明文多一个 16 字节的 GCM tag：整个文件的上限要给它们留出这点
	// 余量，否则装到上限的加密 band 会被「文件太大」挡回来。
	bandCipherOverhead = 1 << 10

	bandCipherAlgo      = "AES-128-GCM-STREAM"
	bandKeySize         = 16 // AES-128
	bandNoncePrefixSize = 8
	bandNonceSize       = 12 // prefix(8) + BE32(块序号)
	bandWrapInfo        = "netdream-band-wrap-v1"
)

// errBandNotForMe 表示加密 band 里没有用本机公钥包裹的密钥：这份文件没打算给这台机器。
var errBandNotForMe = errors.New("这份加密工作区不是给你的")

// errBandSignature 表示签名不成立：头被改过，或者签名者不是它自称的那个人。
var errBandSignature = errors.New("工作区的签名不成立")

// errBandDigest 表示 zip 区域的摘要对不上：内容被改过。
var errBandDigest = errors.New("工作区的内容与头里的摘要不符")

// errBandCipher 表示某个密文块没通过 AEAD 校验。
var errBandCipher = errors.New("工作区的加密区域没有通过校验")

// bandContainerLimits 是 v2 容器的几条限额，做成字段好让测试用小限额验证超限路径
// （同 bandBudget 的做法）。
type bandContainerLimits struct {
	maxHeader int64
	maxRegion int64
	chunkSize int
}

func defaultBandContainerLimits() bandContainerLimits {
	return bandContainerLimits{
		maxHeader: bandMaxHeaderSize,
		maxRegion: bandMaxRegionSize,
		chunkSize: bandChunkSize,
	}
}

// bandIdentity 是 header 里签发者的公钥与展示信息。
type bandIdentity struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Ed25519     string `json:"ed25519"`
	ECDH        string `json:"ecdh"`
	Fingerprint string `json:"fingerprint"`
}

// bandKeyEntry 是一个收件人的包裹：hash 是收方 X25519 公钥的 sha256，
// wrapped = nonce(12B) ‖ AES-128-GCM(wrapKey, zip 密钥)。
type bandKeyEntry struct {
	Hash    string `json:"hash"`
	Wrapped string `json:"wrapped"`
}

type bandCipher struct {
	Algo        string `json:"algo"`
	ChunkSize   int    `json:"chunkSize"`
	NoncePrefix string `json:"noncePrefix"`
}

type bandZipInfo struct {
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// bandHeader 是 v2 容器的明文头。字段顺序固定，签名就是它的规范 JSON：
// 重新 marshal 出来逐字节比对，未知字段由解码时的 DisallowUnknownFields 挡掉。
type bandHeader struct {
	Format    string         `json:"format"`
	Version   int            `json:"version"`
	Identity  *bandIdentity  `json:"identity,omitempty"`
	Encrypted bool           `json:"encrypted"`
	EphPub    string         `json:"ephPub,omitempty"`
	Keys      []bandKeyEntry `json:"keys,omitempty"`
	Cipher    *bandCipher    `json:"cipher,omitempty"`
	Zip       bandZipInfo    `json:"zip"`
	SignedAt  int64          `json:"signedAt"`
	Signature string         `json:"signature,omitempty"`
}

// bandEnvelope 是一个加密 band 的密钥信封。重保存时原样沿用 zipKey + ephPub +
// keys[]：能解密就必然能再加密，收件人不变（noncePrefix 每次重写都换新的）。
type bandEnvelope struct {
	ephPub []byte
	zipKey []byte
	keys   []bandKeyEntry
}

// bandEnvelopes 记住已经载入的加密 band 的信封：加密保存时没另选收件人就用它。
var bandEnvelopes = struct {
	sync.Mutex
	m map[string]*bandEnvelope
}{m: map[string]*bandEnvelope{}}

// bandEnvelopeKey 让人手写的相对路径和绝对路径落到同一个信封上。
func bandEnvelopeKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// rememberBandEnvelope 记下 path 的信封；env 为 nil（明文）时清掉旧信封，
// 免得下次加密保存悄悄用上不该再用的密钥。
func rememberBandEnvelope(path string, env *bandEnvelope) {
	key := bandEnvelopeKey(path)
	bandEnvelopes.Lock()
	defer bandEnvelopes.Unlock()
	if env == nil {
		delete(bandEnvelopes.m, key)
		return
	}
	bandEnvelopes.m[key] = env
}

func recalledBandEnvelope(path string) *bandEnvelope {
	key := bandEnvelopeKey(path)
	bandEnvelopes.Lock()
	defer bandEnvelopes.Unlock()
	return bandEnvelopes.m[key]
}

func (identity *bandIdentity) candidate() IdentityCandidate {
	return IdentityCandidate{
		ID:          identity.ID,
		Email:       identity.Email,
		Fingerprint: identity.Fingerprint,
		ECDH:        identity.ECDH,
	}
}

func (identity *bandIdentity) cached() cachedIdentity {
	return cachedIdentity{
		ID:          identity.ID,
		Email:       identity.Email,
		Fingerprint: identity.Fingerprint,
		ECDH:        identity.ECDH,
		Ed25519:     identity.Ed25519,
	}
}

// trustedRow 把 header 里的身份转成 trusted.json 的一行。
func (identity *bandIdentity) trustedRow() TrustedIdentity {
	return TrustedIdentity{
		Fingerprint: identity.Fingerprint,
		ID:          identity.ID,
		Email:       identity.Email,
		Ed25519:     identity.Ed25519,
		ECDH:        identity.ECDH,
		AddedAt:     time.Now().UnixMilli(),
	}
}

// sniffBand 只看前 4 字节决定容器：v1 是裸 zip（"PK\x03\x04"），
// v2 是 "NDB2"。
func sniffBand(path string) (int, error) {
	name := filepath.Base(path)
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("打开 %s 失败：%w", name, err)
	}
	defer file.Close()

	head := make([]byte, 4)
	if _, err := io.ReadFull(file, head); err != nil {
		return 0, fmt.Errorf("%s 不是 NetDream 工作区：文件太短", name)
	}
	switch {
	case string(head) == bandMagicV2:
		return bandContainerV2, nil
	case bytes.Equal(head, []byte("PK\x03\x04")):
		return bandContainerV1, nil
	}
	return 0, fmt.Errorf("%s 不是 NetDream 工作区：无法识别的文件头", name)
}

// parseBandHeader 读 magic、头长度和头，返回头和 zip 区域的起点。
func parseBandHeader(file *os.File, name string, limits bandContainerLimits) (*bandHeader, int64, error) {
	fixed := make([]byte, 8)
	if _, err := io.ReadFull(file, fixed); err != nil {
		return nil, 0, fmt.Errorf("%s 不是 NetDream 工作区：文件太短", name)
	}
	if string(fixed[:4]) != bandMagicV2 {
		return nil, 0, fmt.Errorf("%s 不是 v2 容器", name)
	}
	headerLen := int64(binary.LittleEndian.Uint32(fixed[4:]))
	if headerLen <= 0 || headerLen > limits.maxHeader {
		return nil, 0, fmt.Errorf("%w：%s 的容器头有 %d 字节，上限 %d 字节",
			errBandLimit, name, headerLen, limits.maxHeader)
	}
	body := make([]byte, headerLen)
	if _, err := io.ReadFull(file, body); err != nil {
		return nil, 0, fmt.Errorf("读取 %s 的容器头失败：%w", name, err)
	}

	var header bandHeader
	decoder := json.NewDecoder(bytes.NewReader(body))
	// 头里出现不认识的字段时签名一定对不上：与其带着未知字段继续，不如当场拒绝。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return nil, 0, fmt.Errorf("%s 的容器头无法解析：%w", name, err)
	}
	if header.Format != bandFormatV2 || header.Version != bandContainerVersion {
		return nil, 0, fmt.Errorf("%s 的容器格式是 %q v%d，只支持 %q v%d",
			name, header.Format, header.Version, bandFormatV2, bandContainerVersion)
	}
	return &header, 8 + headerLen, nil
}

// bandHeaderPayload 是签名的原文：除 signature 外全部字段。结构体字段顺序固定，
// 所以重新 marshal 是确定的；header 里任何改动都会让签名对不上（fail closed）。
func bandHeaderPayload(header *bandHeader) ([]byte, error) {
	canonical := *header
	canonical.Signature = ""
	body, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("编码工作区容器头失败：%w", err)
	}
	return body, nil
}

// signBandHeader 用自己的身份给头签名。
func signBandHeader(header *bandHeader, priv ed25519.PrivateKey) (string, error) {
	body, err := bandHeaderPayload(header)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)), nil
}

// verifyBandHeader 验签，并核对身份的自称指纹与公钥是否一致。身份或签名缺一
// 即为未签名（v1、未建立身份时写出的文件），交给调用方决定能不能接受。
func verifyBandHeader(header *bandHeader, name string) error {
	if header.Identity == nil || strings.TrimSpace(header.Signature) == "" {
		return nil
	}
	pub, err := decodeKey(header.Identity.Ed25519, ed25519.PublicKeySize)
	if err != nil {
		return fmt.Errorf("%s 的签名者公钥不合法：%w", name, err)
	}
	if !strings.EqualFold(strings.TrimSpace(header.Identity.Fingerprint), identityFingerprint(pub)) {
		return fmt.Errorf("%w：%s 自称的指纹与它的公钥不符", errBandSignature, name)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header.Signature))
	if err != nil {
		return fmt.Errorf("%s 的签名无法解码：%w", name, err)
	}
	body, err := bandHeaderPayload(header)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, body, signature) {
		return fmt.Errorf("%w：%s 的容器头被改过", errBandSignature, name)
	}
	return nil
}

// readBandHeader 只读一份 v2 的头并验签：导入公钥用——不需要本机密钥，
// 也不做信任判定（公钥本来就是要手动导入的东西）。
func readBandHeader(path string, limits bandContainerLimits) (*bandHeader, error) {
	name := filepath.Base(path)
	version, err := sniffBand(path)
	if err != nil {
		return nil, err
	}
	if version != bandContainerV2 {
		return nil, fmt.Errorf("%s 是 v1 归档，没有签发者身份", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败：%w", name, err)
	}
	defer file.Close()

	header, _, err := parseBandHeader(file, name, limits)
	if err != nil {
		return nil, err
	}
	if err := verifyBandHeader(header, name); err != nil {
		return nil, err
	}
	return header, nil
}

// bandRead 是一次 v2 读盘的中间状态：头已经解出，zip 区域还没碰。挂起等信任时
// 保存的就是它——只含路径与头的解读结果，零解密成本。
type bandRead struct {
	path         string
	name         string
	header       bandHeader
	regionOffset int64
	regionSize   int64
	envelope     *bandEnvelope // 加密且已用本机私钥解出 zip 密钥时非空
	prompt       *TrustPrompt  // 非空 = 签名者身份未知，等 ResolveTrust
	limits       bandContainerLimits
}

// signerFingerprint 是签发者的指纹；身份或签名缺一即为未签名，返回空串
// （v1 与未建立身份时保存的文件都不带签名，不该因此弹信任窗）。
func (read *bandRead) signerFingerprint() string {
	if read.header.Identity == nil || strings.TrimSpace(read.header.Signature) == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(read.header.Identity.Fingerprint))
}

// stageBandRead 走读盘管线的上半段：解头 → 加密先查包裹 → 验签 → 信任判定。
// 返回的 read.prompt 非空表示要挂起等用户确认；确认后由 finishBandRead 收尾。
func stageBandRead(path string, limits bandContainerLimits) (*bandRead, error) {
	name := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败：%w", name, err)
	}
	maxFile := limits.maxRegion + limits.maxHeader + bandCipherOverhead
	if info.Size() > maxFile {
		return nil, fmt.Errorf("%w：%s 有 %d MiB，上限 %d MiB",
			errBandLimit, name, info.Size()>>20, maxFile>>20)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败：%w", name, err)
	}
	defer file.Close()

	header, offset, err := parseBandHeader(file, name, limits)
	if err != nil {
		return nil, err
	}
	read := &bandRead{
		path:         path,
		name:         name,
		header:       *header,
		regionOffset: offset,
		regionSize:   info.Size() - offset,
		limits:       limits,
	}
	if read.regionSize < 0 {
		return nil, fmt.Errorf("%s 的容器头长过了文件本身", name)
	}

	// 加密的先查信封：解不开就不必验签，也不该让未知身份白看头里的东西。
	if header.Encrypted {
		envelope, err := unwrapBandEnvelope(header, name)
		if err != nil {
			return nil, err
		}
		read.envelope = envelope
	}
	if err := verifyBandHeader(header, name); err != nil {
		return nil, err
	}
	if header.Encrypted && (header.Identity == nil || strings.TrimSpace(header.Signature) == "") {
		// 没有签名的加密 band 无法确认是谁写给谁的，也就无从信任。
		return nil, fmt.Errorf("%s 已加密但没有签名：拒绝打开", name)
	}
	if !isPersistentlyTrusted(read.signerFingerprint()) {
		read.prompt = &TrustPrompt{
			Path:        path,
			ID:          header.Identity.ID,
			Email:       header.Identity.Email,
			Fingerprint: read.signerFingerprint(),
		}
	}
	return read, nil
}

// finishBandRead 走读盘管线的下半段：区域 spool 到临时文件（解密或直读，边读
// 边算摘要）→ 核对摘要 → 解 zip 条目。用完的临时文件先关后删（Windows）。
func finishBandRead(read *bandRead) (WorkspaceData, error) {
	if !isTrustedSigner(read.signerFingerprint()) {
		return WorkspaceData{}, fmt.Errorf("%s 的签名者没有被信任", read.name)
	}

	spool, err := os.CreateTemp("", "ndband-*.zip")
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("创建工作区临时文件失败：%w", err)
	}
	spoolName := spool.Name()
	defer func() {
		spool.Close()
		os.Remove(spoolName)
	}()

	source, err := os.Open(read.path)
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("打开 %s 失败：%w", read.name, err)
	}
	defer source.Close()
	if _, err := source.Seek(read.regionOffset, io.SeekStart); err != nil {
		return WorkspaceData{}, fmt.Errorf("读取 %s 失败：%w", read.name, err)
	}

	digest := sha256.New()
	plain := &bandLimitWriter{w: io.MultiWriter(spool, digest), max: read.limits.maxRegion, name: read.name}
	region := io.LimitReader(source, read.regionSize)
	if read.envelope != nil {
		if err := decryptBandRegion(region, plain, read.envelope.zipKey, &read.header, read.name); err != nil {
			return WorkspaceData{}, err
		}
	} else if _, err := io.Copy(plain, region); err != nil {
		return WorkspaceData{}, fmt.Errorf("读取 %s 的 zip 区域失败：%w", read.name, err)
	}

	if plain.written != read.header.Zip.Size {
		return WorkspaceData{}, fmt.Errorf("%w：%s 的 zip 区域有 %d 字节，头里写的是 %d 字节",
			errBandDigest, read.name, plain.written, read.header.Zip.Size)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != strings.ToLower(strings.TrimSpace(read.header.Zip.Digest)) {
		return WorkspaceData{}, fmt.Errorf("%w：%s 的 zip 区域被改过", errBandDigest, read.name)
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return WorkspaceData{}, fmt.Errorf("读取工作区临时文件失败：%w", err)
	}
	zr, err := zip.NewReader(spool, plain.written)
	if err != nil {
		return WorkspaceData{}, fmt.Errorf("%s 里的 zip 区域无法解析：%w", read.name, err)
	}
	data, err := readBandEntries(zr, read.name)
	if err != nil {
		return WorkspaceData{}, err
	}

	// 记下签发者：加密保存的收件人候选离不开它。
	if read.header.Identity != nil {
		_ = cacheIdentity(read.header.Identity.cached())
	}
	// 记住信封（明文则清掉）：下次加密保存沿用同一份收件人。
	rememberBandEnvelope(read.path, read.envelope)
	return data, nil
}

// writeBandContainer 把 data 写成 v2 容器：zip 流进区域临时文件（需要加密就套
// 分块加密写入器，同时算明文摘要），再用自己的身份签头，最后写最终临时文件
// = magic + 头长 + 头 + 区域拷贝，rename 替换。失败路径先关后删。
func writeBandContainer(path string, data WorkspaceData, identity *localIdentity, envelope *bandEnvelope, limits bandContainerLimits) error {
	bandWriteMu.Lock()
	defer bandWriteMu.Unlock()

	name := filepath.Base(path)
	dir := filepath.Dir(path)

	area, err := os.CreateTemp(dir, "."+name+".*.part")
	if err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	areaName := area.Name()
	defer func() {
		area.Close()
		os.Remove(areaName)
	}()

	digest := sha256.New()
	sink := io.Writer(area)
	var stream *bandStreamWriter
	if envelope != nil {
		prefix := make([]byte, bandNoncePrefixSize)
		if _, err := io.ReadFull(rand.Reader, prefix); err != nil {
			return fmt.Errorf("生成加密随机数失败：%w", err)
		}
		stream, err = newBandStreamWriter(area, envelope.zipKey, limits.chunkSize, prefix)
		if err != nil {
			return err
		}
		sink = stream
	}
	// 摘要覆盖的是 zip 明文：写盘时在压缩输出侧顺路算，不额外读一遍。
	plain := &bandLimitWriter{w: io.MultiWriter(digest, sink), max: limits.maxRegion, name: name}

	zw := zip.NewWriter(plain)
	if err := writeBandEntries(zw, data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("写入工作区归档失败：%w", err)
	}
	if stream != nil {
		if err := stream.Close(); err != nil {
			return err
		}
	}
	if err := area.Close(); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}

	header := bandHeader{
		Format:    bandFormatV2,
		Version:   bandContainerVersion,
		Encrypted: envelope != nil,
		Zip: bandZipInfo{
			Size:   plain.written,
			Digest: hex.EncodeToString(digest.Sum(nil)),
		},
		SignedAt: time.Now().UnixMilli(),
	}
	if envelope != nil {
		header.EphPub = base64.StdEncoding.EncodeToString(envelope.ephPub)
		header.Keys = envelope.keys
		header.Cipher = &bandCipher{
			Algo:        bandCipherAlgo,
			ChunkSize:   limits.chunkSize,
			NoncePrefix: base64.StdEncoding.EncodeToString(stream.prefix),
		}
	}
	if identity != nil {
		header.Identity = identity.bandIdentity()
		signature, err := signBandHeader(&header, identity.Ed25519Priv)
		if err != nil {
			return err
		}
		header.Signature = signature
	}

	headerBody, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("编码工作区容器头失败：%w", err)
	}
	if int64(len(headerBody)) > limits.maxHeader {
		return fmt.Errorf("%w：工作区容器头有 %d 字节，上限 %d 字节", errBandLimit, len(headerBody), limits.maxHeader)
	}

	areaFile, err := os.Open(areaName)
	if err != nil {
		return fmt.Errorf("读取工作区临时文件失败：%w", err)
	}
	defer areaFile.Close()

	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		tmp.Close()
		if !done {
			os.Remove(tmpName) // 替换成功之前失败：别留下半截临时文件
		}
	}()

	var headerLen [4]byte
	binary.LittleEndian.PutUint32(headerLen[:], uint32(len(headerBody)))
	for _, part := range [][]byte{[]byte(bandMagicV2), headerLen[:], headerBody} {
		if _, err := tmp.Write(part); err != nil {
			return fmt.Errorf("写入 %s 失败：%w", name, err)
		}
	}
	if _, err := io.Copy(tmp, areaFile); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	// os.CreateTemp 建的是 0600，写出来的归档保持原来直接写的可读权限。
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换 %s 失败：%w", name, err)
	}
	done = true
	return nil
}

// bandLimitWriter 数着流过的明文字节：超限当场报错，别把整个 archive 写满磁盘
// 才发现太大。
type bandLimitWriter struct {
	w       io.Writer
	max     int64
	written int64
	name    string
}

func (limit *bandLimitWriter) Write(p []byte) (int, error) {
	if limit.written+int64(len(p)) > limit.max {
		return 0, fmt.Errorf("%w：%s 的内容超过 %d MiB", errBandLimit, limit.name, limit.max>>20)
	}
	n, err := limit.w.Write(p)
	limit.written += int64(n)
	return n, err
}

// bandStreamWriter 把明文按块加密写进 dst：每块 nonce(12B) ‖ AES-128-GCM 密文。
// 分块让内存只占一块，也让篡改在块粒度上就露馅。
type bandStreamWriter struct {
	dst       io.Writer
	gcm       cipher.AEAD
	prefix    []byte
	chunkSize int
	buf       []byte
	seq       uint32
}

func newBandStreamWriter(dst io.Writer, key []byte, chunkSize int, prefix []byte) (*bandStreamWriter, error) {
	if chunkSize <= 0 {
		chunkSize = bandChunkSize
	}
	if chunkSize > bandMaxChunkSize {
		return nil, fmt.Errorf("加密块大小 %d 字节超过上限 %d 字节", chunkSize, bandMaxChunkSize)
	}
	if len(prefix) != bandNoncePrefixSize {
		return nil, fmt.Errorf("加密 nonce 前缀长度是 %d 字节，应为 %d 字节", len(prefix), bandNoncePrefixSize)
	}
	gcm, err := newBandGCM(key)
	if err != nil {
		return nil, err
	}
	return &bandStreamWriter{
		dst:       dst,
		gcm:       gcm,
		prefix:    prefix,
		chunkSize: chunkSize,
		buf:       make([]byte, 0, chunkSize),
	}, nil
}

func (stream *bandStreamWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := len(p)
		if room := stream.chunkSize - len(stream.buf); n > room {
			n = room
		}
		stream.buf = append(stream.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(stream.buf) == stream.chunkSize {
			if err := stream.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Close 写出最后一块不满的明文。
func (stream *bandStreamWriter) Close() error {
	return stream.flush()
}

func (stream *bandStreamWriter) flush() error {
	if len(stream.buf) == 0 {
		return nil
	}
	nonce := bandNonce(stream.prefix, stream.seq)
	sealed := stream.gcm.Seal(nil, nonce, stream.buf, nil)
	if _, err := stream.dst.Write(nonce); err != nil {
		return fmt.Errorf("写入工作区加密区域失败：%w", err)
	}
	if _, err := stream.dst.Write(sealed); err != nil {
		return fmt.Errorf("写入工作区加密区域失败：%w", err)
	}
	stream.seq++
	stream.buf = stream.buf[:0]
	return nil
}

// decryptBandRegion 逐块解密 zip 区域：块 tag 不对即报错（截断、篡改、错钥都在
// 块粒度上败下来），解出的明文写进 dst。
func decryptBandRegion(region io.Reader, dst io.Writer, key []byte, header *bandHeader, name string) error {
	if header.Cipher == nil {
		return fmt.Errorf("%s 说自己是加密的，却没有 cipher 信息", name)
	}
	if header.Cipher.Algo != bandCipherAlgo {
		return fmt.Errorf("%s 的加密算法是 %q，只支持 %q", name, header.Cipher.Algo, bandCipherAlgo)
	}
	chunkSize := header.Cipher.ChunkSize
	if chunkSize <= 0 {
		chunkSize = bandChunkSize
	}
	if chunkSize > bandMaxChunkSize {
		return fmt.Errorf("%s 的加密块大小 %d 字节超过上限 %d 字节", name, chunkSize, bandMaxChunkSize)
	}
	prefix, err := decodeKey(header.Cipher.NoncePrefix, bandNoncePrefixSize)
	if err != nil {
		return fmt.Errorf("%s 的加密 nonce 前缀不合法：%w", name, err)
	}
	gcm, err := newBandGCM(key)
	if err != nil {
		return err
	}

	remaining := header.Zip.Size
	// 每块在盘上是 nonce(12B) ‖ 密文(明文 + 16B tag)，读的时候一次性读进来。
	block := make([]byte, 0, bandNonceSize+chunkSize+16)
	for seq := uint32(0); remaining > 0; seq++ {
		size := int64(chunkSize)
		if remaining < size {
			size = remaining
		}
		block = block[:bandNonceSize+size+16]
		if _, err := io.ReadFull(region, block); err != nil {
			return fmt.Errorf("%s 的加密区域不完整：%w", name, err)
		}
		plain, err := gcm.Open(nil, bandNonce(prefix, seq), block[bandNonceSize:], nil)
		if err != nil {
			return fmt.Errorf("%w：%s 的加密区域第 %d 块被改过或密钥不符", errBandCipher, name, seq+1)
		}
		if _, err := dst.Write(plain); err != nil {
			return fmt.Errorf("写入工作区临时文件失败：%w", err)
		}
		remaining -= size
	}
	// 头里声明的明文长度之后不该还有密文。
	if extra, _ := io.CopyN(io.Discard, region, 1); extra != 0 {
		return fmt.Errorf("%w：%s 的加密区域尾部多出字节", errBandCipher, name)
	}
	return nil
}

// bandNonce 是块 nonce：文件级前缀(8B) ‖ 块序号 BE32。序号由块位置决定，
// 所以同一 zipKey 下不同文件的 nonce 不重复，块也能按序号随机访问解密。
func bandNonce(prefix []byte, seq uint32) []byte {
	nonce := make([]byte, bandNonceSize)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[bandNoncePrefixSize:], seq)
	return nonce
}

func newBandGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != bandKeySize {
		return nil, fmt.Errorf("工作区密钥长度是 %d 字节，应为 %d 字节", len(key), bandKeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化工作区加密失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化工作区加密失败：%w", err)
	}
	return gcm, nil
}

// hkdfSHA256 是 RFC 5869 的最小子集：标准库没有 HKDF（go.mod 不引 x/crypto），
// 而这里只需要「一个 salt、一个 info、输出不超过一轮」。expand 用 HMAC 链，
// 与 RFC 的 T(i) = HMAC(PRK, T(i-1) ‖ info ‖ i) 一致。
func hkdfSHA256(secret, salt []byte, info string, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	extract.Write(secret)
	prk := extract.Sum(nil)

	out := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(out) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		expand.Write(previous)
		expand.Write([]byte(info))
		expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		out = append(out, previous...)
	}
	return out[:length]
}

// buildBandEnvelope 为一次加密保存造新信封：临时 X25519 密钥对 + 每个收件人
// 一个包裹。保存者自己的公钥总会加上——保存者应该总能打开自己的加密文件。
func buildBandEnvelope(identity *localIdentity, recipients []string) (*bandEnvelope, error) {
	zipKey := make([]byte, bandKeySize)
	if _, err := io.ReadFull(rand.Reader, zipKey); err != nil {
		return nil, fmt.Errorf("生成工作区密钥失败：%w", err)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成临时加密密钥失败：%w", err)
	}

	pubs := make([][]byte, 0, len(recipients)+1)
	seen := map[string]bool{}
	for _, text := range recipients {
		pub, err := decodeKey(text, 32)
		if err != nil {
			return nil, fmt.Errorf("收件人公钥不合法：%w", err)
		}
		hash := identityFingerprint(pub)
		if seen[hash] {
			continue
		}
		seen[hash] = true
		pubs = append(pubs, pub)
	}
	if own := identity.ECDHPub.Bytes(); !seen[identityFingerprint(own)] {
		pubs = append(pubs, own)
	}

	keys := make([]bandKeyEntry, 0, len(pubs))
	for _, pub := range pubs {
		boxed, err := wrapBandKey(ephemeral, pub, zipKey)
		if err != nil {
			return nil, err
		}
		keys = append(keys, boxed)
	}
	return &bandEnvelope{
		ephPub: ephemeral.PublicKey().Bytes(),
		zipKey: zipKey,
		keys:   keys,
	}, nil
}

// wrapBandKey 把一个收件人的公钥包成 keys[] 的一行。
func wrapBandKey(ephemeral *ecdh.PrivateKey, recipientPub, zipKey []byte) (bandKeyEntry, error) {
	pub, err := ecdh.X25519().NewPublicKey(recipientPub)
	if err != nil {
		return bandKeyEntry{}, fmt.Errorf("收件人公钥不合法：%w", err)
	}
	shared, err := ephemeral.ECDH(pub)
	if err != nil {
		return bandKeyEntry{}, fmt.Errorf("与收件人协商密钥失败：%w", err)
	}
	wrapKey := hkdfSHA256(shared, ephemeral.PublicKey().Bytes(), bandWrapInfo, bandKeySize)
	gcm, err := newBandGCM(wrapKey)
	if err != nil {
		return bandKeyEntry{}, err
	}
	nonce := make([]byte, bandNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return bandKeyEntry{}, fmt.Errorf("生成随机 nonce 失败：%w", err)
	}
	wrapped := append(nonce, gcm.Seal(nil, nonce, zipKey, nil)...)
	return bandKeyEntry{
		Hash:    identityFingerprint(recipientPub),
		Wrapped: base64.StdEncoding.EncodeToString(wrapped),
	}, nil
}

// unwrapBandEnvelope 在 keys[] 里找用本机公钥包裹的那一行，用本机私钥解出
// zip 密钥。找不到 = 这份文件没打算给这台机器。
func unwrapBandEnvelope(header *bandHeader, name string) (*bandEnvelope, error) {
	local, err := loadLocalIdentity()
	if err != nil {
		return nil, err
	}
	if local == nil {
		return nil, fmt.Errorf("%w：本机还没有身份，无法打开加密的工作区", errBandNotForMe)
	}
	ephPub, err := decodeKey(header.EphPub, 32)
	if err != nil {
		return nil, fmt.Errorf("%s 的临时公钥不合法：%w", name, err)
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephPub)
	if err != nil {
		return nil, fmt.Errorf("%s 的临时公钥不合法：%w", name, err)
	}
	shared, err := local.ECDHPriv.ECDH(ephemeral)
	if err != nil {
		return nil, fmt.Errorf("%s 的临时公钥无法协商：%w", name, err)
	}
	wrapKey := hkdfSHA256(shared, ephPub, bandWrapInfo, bandKeySize)
	gcm, err := newBandGCM(wrapKey)
	if err != nil {
		return nil, err
	}

	mine := identityFingerprint(local.ECDHPub.Bytes())
	for _, entry := range header.Keys {
		if !strings.EqualFold(strings.TrimSpace(entry.Hash), mine) {
			continue
		}
		wrapped, err := base64.StdEncoding.DecodeString(strings.TrimSpace(entry.Wrapped))
		if err != nil || len(wrapped) < bandNonceSize {
			return nil, fmt.Errorf("%s 里给本机的密钥包裹不合法", name)
		}
		zipKey, err := gcm.Open(nil, wrapped[:bandNonceSize], wrapped[bandNonceSize:], nil)
		if err != nil {
			return nil, fmt.Errorf("%s 里给本机的密钥包裹打不开", name)
		}
		if len(zipKey) != bandKeySize {
			return nil, fmt.Errorf("%s 里给本机的密钥长度不合法", name)
		}
		return &bandEnvelope{ephPub: ephPub, zipKey: zipKey, keys: header.Keys}, nil
	}
	return nil, fmt.Errorf("%w：%s 的收件人里没有本机公钥", errBandNotForMe, name)
}
