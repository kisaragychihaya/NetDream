package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// testWorkspace 是一份带分组、变量和两条请求的工作区，容器往返测试的样本。
func testWorkspace() WorkspaceData {
	return WorkspaceData{
		Name: "容器测试",
		Requests: []SavedRequest{
			testRequest("r1", "第一条"),
			testRequest("r2", "第二条"),
		},
		Vars:    map[string]string{"host": "localhost"},
		Folders: []SavedFolder{{ID: "f1", Name: "集合", Requests: []string{"r1"}}},
	}
}

// bigWorkspace 造一份压不下去的工作区：base64 的随机字节 deflate 后仍比几十个
// 块大，用来确认多块加密是逐块接起来的。
func bigWorkspace(t *testing.T, size int) WorkspaceData {
	t.Helper()
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("生成随机内容失败：%v", err)
	}
	request := testRequest("r1", "大请求")
	request.Raw = base64.StdEncoding.EncodeToString(raw)
	return WorkspaceData{
		Name:     "大工作区",
		Requests: []SavedRequest{request},
		Vars:     map[string]string{"blob": base64.StdEncoding.EncodeToString(raw[:64])},
		// 读侧总把缺省的 folders 补成 []，样本也照此填，比较才不假红。
		Folders: []SavedFolder{},
	}
}

// readTestHeader 解出容器头，并给出 zip 区域的起点。
func readTestHeader(t *testing.T, path string) (*bandHeader, int64) {
	t.Helper()
	body := readFileBytes(t, path)
	if len(body) < 8 || string(body[:4]) != bandMagicV2 {
		t.Fatalf("%s 不是 v2 容器", filepath.Base(path))
	}
	headerLen := int64(binary.LittleEndian.Uint32(body[4:8]))
	var header bandHeader
	if err := json.Unmarshal(body[8:8+headerLen], &header); err != nil {
		t.Fatalf("容器头无法解析：%v", err)
	}
	return &header, 8 + headerLen
}

// flipByteAt 改掉文件里一个字节：模拟被改过的 band。
func flipByteAt(t *testing.T, path string, offset int64) {
	t.Helper()
	body := readFileBytes(t, path)
	if offset < 0 || offset >= int64(len(body)) {
		t.Fatalf("偏移 %d 越界（文件 %d 字节）", offset, len(body))
	}
	body[offset] ^= 0xff
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("写回 %s 失败：%v", path, err)
	}
}

// v2 明文往返：没有身份时写出的是未签名容器，读回来不弹信任窗。
func TestBandContainerPlainRoundTrip(t *testing.T) {
	useTempConfig(t)
	path := filepath.Join(t.TempDir(), "plain.band")
	want := testWorkspace()

	if err := writeBandContainer(path, want, nil, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}
	if body := readFileBytes(t, path); !bytes.HasPrefix(body, []byte(bandMagicV2)) {
		t.Fatalf("文件头 = %q, want %q", body[:4], bandMagicV2)
	}
	header, _ := readTestHeader(t, path)
	if header.Encrypted || header.Identity != nil || header.Signature != "" {
		t.Fatalf("明文容器头 = %#v, want 未加密未签名", header)
	}

	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if opened.Trust != nil {
		t.Fatalf("未签名的容器不该问信任：%#v", opened.Trust)
	}
	if opened.File.Path != path {
		t.Fatalf("path = %q, want %q", opened.File.Path, path)
	}
	if opened.File.SignatureTrusted {
		t.Fatalf("未签名的容器不该标记 SignatureTrusted")
	}
	if !reflect.DeepEqual(opened.File.Data, want) {
		t.Fatalf("往返之后 = %#v, want %#v", opened.File.Data, want)
	}
}

// 用自己的身份签名的 v2 容器：自己是可信的，直接读出数据。
func TestBandContainerSignedRoundTrip(t *testing.T) {
	useTempConfig(t)
	me := newTestIdentity(t, "me@example.com")
	installIdentity(t, me)

	path := filepath.Join(t.TempDir(), "signed.band")
	want := testWorkspace()
	if err := writeBandContainer(path, want, me, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}
	header, _ := readTestHeader(t, path)
	if header.Identity == nil || header.Signature == "" {
		t.Fatalf("容器头没有签名：%#v", header)
	}
	if header.Identity.Fingerprint != me.fingerprint() {
		t.Fatalf("签名者指纹 = %q, want %q", header.Identity.Fingerprint, me.fingerprint())
	}

	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if opened.Trust != nil {
		t.Fatalf("自己的签名不该问信任：%#v", opened.Trust)
	}
	if !opened.File.SignatureTrusted {
		t.Fatalf("已验签且签名者可信，SignatureTrusted 应为 true")
	}
	if !reflect.DeepEqual(opened.File.Data, want) {
		t.Fatalf("往返之后 = %#v, want %#v", opened.File.Data, want)
	}
}

// 改掉容器头里的摘要一个字符：签名覆盖它，验签必须失败（fail closed）。
func TestBandContainerRejectsTamperedHeader(t *testing.T) {
	useTempConfig(t)
	me := newTestIdentity(t, "me@example.com")
	installIdentity(t, me)

	path := filepath.Join(t.TempDir(), "tampered-header.band")
	if err := writeBandContainer(path, testWorkspace(), me, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	body := readFileBytes(t, path)
	headerLen := int64(binary.LittleEndian.Uint32(body[4:8]))
	headerBody := body[8 : 8+headerLen]
	marker := []byte(`"digest":"`)
	at := bytes.Index(headerBody, marker)
	if at < 0 {
		t.Fatalf("容器头里没有 digest 字段：%s", headerBody)
	}
	// 换成另一个合法的十六进制字符：JSON 依然能解析，只有签名对不上。
	offset := int64(8+at) + int64(len(marker))
	if body[offset] == 'f' {
		body[offset] = 'e'
	} else {
		body[offset] = 'f'
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("写回 %s 失败：%v", path, err)
	}

	if _, err := openWorkspace(path); !errors.Is(err, errBandSignature) {
		t.Fatalf("openWorkspace error = %v, want errBandSignature", err)
	}
}

// 改掉签名容器里 zip 区域的一个字节：摘要不符，必须报错。
func TestBandContainerRejectsTamperedRegion(t *testing.T) {
	useTempConfig(t)
	me := newTestIdentity(t, "me@example.com")
	installIdentity(t, me)

	path := filepath.Join(t.TempDir(), "tampered-region.band")
	if err := writeBandContainer(path, testWorkspace(), me, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}
	_, regionStart := readTestHeader(t, path)
	flipByteAt(t, path, regionStart+8)

	if _, err := openWorkspace(path); !errors.Is(err, errBandDigest) {
		t.Fatalf("openWorkspace error = %v, want errBandDigest", err)
	}
}

// 未知身份：先挂起返回 TrustPrompt（不解密），确认后才拿到数据。信任一次只对
// 那一次打开生效；信任公钥才写进 trusted.json。
func TestBandContainerTrustFlow(t *testing.T) {
	useTempConfig(t)
	me := newTestIdentity(t, "me@example.com")
	installIdentity(t, me)
	signer := newTestIdentity(t, "signer@example.com")

	path := filepath.Join(t.TempDir(), "signed-by-other.band")
	want := testWorkspace()
	if err := writeBandContainer(path, want, signer, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if opened.Trust == nil {
		t.Fatalf("别人的签名应该先问信任：%#v", opened)
	}
	if opened.File != nil {
		t.Fatalf("信任之前不该有数据：%#v", opened.File)
	}
	prompt := opened.Trust
	if prompt.Path != path || prompt.Fingerprint != signer.fingerprint() ||
		prompt.ID != signer.ID || prompt.Email != signer.Email {
		t.Fatalf("TrustPrompt = %#v, want %s 的身份信息", prompt, signer.fingerprint())
	}

	workspace := WorkspaceService{}
	file, err := workspace.ResolveTrust(prompt.Fingerprint, false)
	if err != nil {
		t.Fatalf("ResolveTrust failed: %v", err)
	}
	if file.Path != path || !reflect.DeepEqual(file.Data, want) {
		t.Fatalf("信任一次之后 = %#v, want %#v", file, want)
	}
	if !file.SignatureTrusted {
		t.Fatalf("信任一次也是信任：SignatureTrusted 应为 true")
	}

	// 「信任一次」取走即失效：再打开仍然要问。
	again, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if again.Trust == nil {
		t.Fatalf("信任一次不该变成持久信任")
	}

	// 指纹不匹配：报错，挂起数据丢掉。
	if _, err := workspace.ResolveTrust("deadbeef", false); err == nil {
		t.Fatalf("指纹不匹配的 ResolveTrust 应该报错")
	}

	// 取消之后没有可完成的读盘。
	if _, err := openWorkspace(path); err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	workspace.DismissTrust()
	if _, err := workspace.ResolveTrust(prompt.Fingerprint, false); err == nil {
		t.Fatalf("DismissTrust 之后 ResolveTrust 应该报错")
	}

	// 信任公钥：写进 trusted.json，之后直读。
	if _, err := openWorkspace(path); err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if _, err := workspace.ResolveTrust(prompt.Fingerprint, true); err != nil {
		t.Fatalf("ResolveTrust(remember) failed: %v", err)
	}
	trusted := (IdentityService{}).Trusted()
	if len(trusted) != 1 || trusted[0].Fingerprint != signer.fingerprint() {
		t.Fatalf("trusted.json = %#v, want the signer", trusted)
	}
	direct, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if direct.Trust != nil {
		t.Fatalf("信任公钥之后还问信任：%#v", direct.Trust)
	}
	if !reflect.DeepEqual(direct.File.Data, want) {
		t.Fatalf("直读的数据 = %#v, want %#v", direct.File.Data, want)
	}
}

// 加密往返：A 写给 B，多块；换成本机身份 B 后能读出原文。
func TestBandContainerEncryptedRoundTrip(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)
	recipient := newTestIdentity(t, "b@example.com")
	limits := bandContainerLimits{maxHeader: bandMaxHeaderSize, maxRegion: bandMaxRegionSize, chunkSize: 64 << 10}

	want := bigWorkspace(t, 256<<10)
	envelope, err := buildBandEnvelope(sender, []string{base64.StdEncoding.EncodeToString(recipient.ECDHPub.Bytes())})
	if err != nil {
		t.Fatalf("buildBandEnvelope failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "secret.band")
	if err := writeBandContainer(path, want, sender, envelope, limits); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	body := readFileBytes(t, path)
	header, regionStart := readTestHeader(t, path)
	if !header.Encrypted || header.Cipher == nil {
		t.Fatalf("容器头 = %#v, want 加密信息", header)
	}
	if header.Cipher.ChunkSize != limits.chunkSize {
		t.Fatalf("块大小 = %d, want %d", header.Cipher.ChunkSize, limits.chunkSize)
	}
	if header.Cipher.NoncePrefix == "" || header.EphPub == "" {
		t.Fatalf("容器头缺 nonce 前缀或临时公钥：%#v", header.Cipher)
	}
	if len(header.Keys) != 2 {
		t.Fatalf("keys = %d 行, want 收件人 + 发件人自己", len(header.Keys))
	}
	if regionSize := int64(len(body)) - regionStart; regionSize <= int64(limits.chunkSize) {
		t.Fatalf("加密区域只有 %d 字节，没有跨块", regionSize)
	}

	// 换成本机身份 = B，并信任签名者 A：加密数据能原样回来。
	installIdentity(t, recipient)
	if err := addTrustedIdentity(sender.bandIdentity().trustedRow()); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if opened.Trust != nil {
		t.Fatalf("已信任的签名者还问信任：%#v", opened.Trust)
	}
	if !reflect.DeepEqual(opened.File.Data, want) {
		t.Fatalf("解密之后 = %#v, want %#v", opened.File.Data, want)
	}
}

// 不是收件人的机器打开加密 band：报「不是给你的」，不做任何解密。
func TestBandContainerEncryptedRejectsOtherIdentity(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)
	recipient := newTestIdentity(t, "b@example.com")
	stranger := newTestIdentity(t, "c@example.com")

	envelope, err := buildBandEnvelope(sender, []string{base64.StdEncoding.EncodeToString(recipient.ECDHPub.Bytes())})
	if err != nil {
		t.Fatalf("buildBandEnvelope failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "not-for-you.band")
	if err := writeBandContainer(path, testWorkspace(), sender, envelope, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	installIdentity(t, stranger)
	if _, err := openWorkspace(path); !errors.Is(err, errBandNotForMe) {
		t.Fatalf("openWorkspace error = %v, want errBandNotForMe", err)
	}
}

// 改掉密文区域一个字节：块 tag 校验失败，报错点名加密区域。
func TestBandContainerRejectsTamperedCiphertext(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)
	recipient := newTestIdentity(t, "b@example.com")

	want := bigWorkspace(t, 128<<10)
	envelope, err := buildBandEnvelope(sender, []string{base64.StdEncoding.EncodeToString(recipient.ECDHPub.Bytes())})
	if err != nil {
		t.Fatalf("buildBandEnvelope failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tampered-cipher.band")
	limits := bandContainerLimits{maxHeader: bandMaxHeaderSize, maxRegion: bandMaxRegionSize, chunkSize: 64 << 10}
	if err := writeBandContainer(path, want, sender, envelope, limits); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	installIdentity(t, recipient)
	if err := addTrustedIdentity(sender.bandIdentity().trustedRow()); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	body := readFileBytes(t, path)
	_, regionStart := readTestHeader(t, path)
	flipByteAt(t, path, regionStart+(int64(len(body))-regionStart)/2)

	if _, err := openWorkspace(path); !errors.Is(err, errBandCipher) {
		t.Fatalf("openWorkspace error = %v, want errBandCipher", err)
	}
}

// 重保存沿用原信封：ephPub 与 keys 不变、nonce 前缀换新，B 与 A 都还能打开。
func TestBandContainerSaveReusesEnvelope(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)
	recipient := newTestIdentity(t, "b@example.com")

	envelope, err := buildBandEnvelope(sender, []string{base64.StdEncoding.EncodeToString(recipient.ECDHPub.Bytes())})
	if err != nil {
		t.Fatalf("buildBandEnvelope failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "reuse.band")
	if err := writeBandContainer(path, testWorkspace(), sender, envelope, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}
	before, _ := readTestHeader(t, path)

	// B 打开（记下信封），再要求加密保存但不指定收件人：应当沿用。
	installIdentity(t, recipient)
	if err := addTrustedIdentity(sender.bandIdentity().trustedRow()); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	if _, err := openWorkspace(path); err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if recalledBandEnvelope(path) == nil {
		t.Fatalf("打开之后没有记住信封")
	}

	workspace := WorkspaceService{}
	next := WorkspaceData{
		Name:     "改过的工作区",
		Requests: []SavedRequest{testRequest("r9", "新的")},
		Vars:     map[string]string{"k": "v"},
		Folders:  []SavedFolder{},
	}
	if err := workspace.Save(path, next, SaveOptions{Encrypt: true}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	after, _ := readTestHeader(t, path)
	if !after.Encrypted || after.Cipher == nil {
		t.Fatalf("重保存之后 = %#v, want 仍然加密", after)
	}
	if after.EphPub != before.EphPub {
		t.Fatalf("ephPub 变了：%q -> %q", before.EphPub, after.EphPub)
	}
	if !reflect.DeepEqual(after.Keys, before.Keys) {
		t.Fatalf("keys 变了：%#v -> %#v", before.Keys, after.Keys)
	}
	if after.Cipher.NoncePrefix == before.Cipher.NoncePrefix {
		t.Fatalf("nonce 前缀没换：%q", after.Cipher.NoncePrefix)
	}

	// B 自己重签的包，直接读。
	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if !reflect.DeepEqual(opened.File.Data, next) {
		t.Fatalf("B 读回 = %#v, want %#v", opened.File.Data, next)
	}

	// A 仍是收件人之一（要先信任 B 的签名）。
	installIdentity(t, sender)
	if err := addTrustedIdentity(recipient.bandIdentity().trustedRow()); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	opened, err = openWorkspace(path)
	if err != nil {
		t.Fatalf("A 打开失败：%v", err)
	}
	if !reflect.DeepEqual(opened.File.Data, next) {
		t.Fatalf("A 读回 = %#v, want %#v", opened.File.Data, next)
	}
}

// 存成明文之后，旧信封作废：再要求加密保存就必须重新选收件人。
func TestBandContainerPlainSaveForgetsEnvelope(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)

	path := filepath.Join(t.TempDir(), "forget.band")
	workspace := WorkspaceService{}
	if err := workspace.Save(path, testWorkspace(), SaveOptions{}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if header, _ := readTestHeader(t, path); header.Encrypted || header.Identity == nil {
		t.Fatalf("明文保存的容器头 = %#v, want 未加密且有签名", header)
	}
	if err := workspace.Save(path, testWorkspace(), SaveOptions{Encrypt: true}); err == nil {
		t.Fatalf("没有信封也没有收件人时的加密保存应该报错")
	}
}

// 限额：头超限、区域超限都要报 errBandLimit。
func TestBandContainerLimits(t *testing.T) {
	useTempConfig(t)
	path := filepath.Join(t.TempDir(), "limits.band")

	small := bandContainerLimits{maxHeader: 64, maxRegion: 1 << 20, chunkSize: 4 << 10}
	if err := writeBandContainer(path, testWorkspace(), nil, nil, small); !errors.Is(err, errBandLimit) {
		t.Fatalf("头超限的写入 error = %v, want errBandLimit", err)
	}

	if err := writeBandContainer(path, testWorkspace(), nil, nil, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}
	read, err := stageBandRead(path, bandContainerLimits{maxHeader: bandMaxHeaderSize, maxRegion: 8, chunkSize: bandChunkSize})
	if err != nil {
		t.Fatalf("stageBandRead failed: %v", err)
	}
	if _, err := finishBandRead(read); !errors.Is(err, errBandLimit) {
		t.Fatalf("区域超限的读取 error = %v, want errBandLimit", err)
	}
}

// 从加密 band 的头里导入签发者公钥：不解密、不信任；v1 归档没有身份可导。
func TestReadBandHeaderForImport(t *testing.T) {
	useTempConfig(t)
	sender := newTestIdentity(t, "a@example.com")
	installIdentity(t, sender)
	recipient := newTestIdentity(t, "b@example.com")

	envelope, err := buildBandEnvelope(sender, []string{base64.StdEncoding.EncodeToString(recipient.ECDHPub.Bytes())})
	if err != nil {
		t.Fatalf("buildBandEnvelope failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "import.band")
	if err := writeBandContainer(path, testWorkspace(), sender, envelope, defaultBandContainerLimits()); err != nil {
		t.Fatalf("writeBandContainer failed: %v", err)
	}

	header, err := readBandHeader(path, defaultBandContainerLimits())
	if err != nil {
		t.Fatalf("readBandHeader failed: %v", err)
	}
	if header.Identity == nil || header.Identity.Fingerprint != sender.fingerprint() {
		t.Fatalf("导入到的身份 = %#v, want 发件人", header.Identity)
	}

	legacy := filepath.Join(t.TempDir(), "legacy.band")
	if err := writeBand(legacy, testWorkspace()); err != nil {
		t.Fatalf("writeBand failed: %v", err)
	}
	if _, err := readBandHeader(legacy, defaultBandContainerLimits()); err == nil {
		t.Fatalf("v1 归档不该导入出身份")
	}
}

// 老归档（裸 zip）仍然走 v1 管线，不验签也不弹信任窗。
func TestOpenWorkspaceReadsV1Archive(t *testing.T) {
	useTempConfig(t)
	path := filepath.Join(t.TempDir(), "old.band")
	want := testWorkspace()
	if err := writeBand(path, want); err != nil {
		t.Fatalf("writeBand failed: %v", err)
	}
	version, err := sniffBand(path)
	if err != nil {
		t.Fatalf("sniffBand failed: %v", err)
	}
	if version != bandContainerV1 {
		t.Fatalf("sniffBand = %d, want v1", version)
	}

	opened, err := openWorkspace(path)
	if err != nil {
		t.Fatalf("openWorkspace failed: %v", err)
	}
	if opened.Trust != nil {
		t.Fatalf("v1 归档不该问信任：%#v", opened.Trust)
	}
	if opened.File.SignatureTrusted {
		t.Fatalf("v1 没有签名，SignatureTrusted 应为 false")
	}
	if !reflect.DeepEqual(opened.File.Data, want) {
		t.Fatalf("v1 读取 = %#v, want %#v", opened.File.Data, want)
	}
}
