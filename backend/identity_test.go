package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempConfig 把身份数据的根目录换成一个临时目录：Windows 上 os.UserConfigDir
// 看 %APPDATA%，类 Unix 上看 XDG_CONFIG_HOME，两个都设上才跨平台隔离得干净。
func useTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// newTestIdentity 造一个只在内存里的身份，测试里当「别人」用。
func newTestIdentity(t *testing.T, email string) *localIdentity {
	t.Helper()
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 ed25519 密钥失败：%v", err)
	}
	ecdhPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 X25519 密钥失败：%v", err)
	}
	return &localIdentity{
		ID:          email,
		Email:       email,
		Ed25519Priv: edPriv,
		Ed25519Pub:  edPub,
		ECDHPriv:    ecdhPriv,
		ECDHPub:     ecdhPriv.PublicKey(),
		CreatedAt:   time.Now().UnixMilli(),
	}
}

// installIdentity 把某个身份写成这台机器的本机身份。
func installIdentity(t *testing.T, local *localIdentity) {
	t.Helper()
	identityMu.Lock()
	defer identityMu.Unlock()
	if err := storeLocalIdentityLocked(local); err != nil {
		t.Fatalf("写入本机身份失败：%v", err)
	}
}

// 首次运行：status 是 unset，Setup 之后有一对能用的密钥，email 带主机名。
func TestIdentitySetupCreatesKeys(t *testing.T) {
	useTempConfig(t)
	service := IdentityService{}

	if got := service.Get(); got.Status != "unset" {
		t.Fatalf("第一次 Get() = %#v, want status unset", got)
	}

	info, err := service.Setup("")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if info.Status != "ready" {
		t.Fatalf("status = %q, want ready", info.Status)
	}
	if info.ID == "" || info.Email == "" {
		t.Fatalf("id/email = %q/%q, want both set", info.ID, info.Email)
	}
	if !strings.Contains(info.Email, "@") {
		t.Fatalf("email = %q, want 名字@主机名", info.Email)
	}
	if host, err := os.Hostname(); err == nil && host != "" && !strings.HasSuffix(info.Email, "@"+host) {
		t.Fatalf("email = %q, want it to end with @%s", info.Email, host)
	}

	edPub, err := base64.StdEncoding.DecodeString(info.Ed25519)
	if err != nil || len(edPub) != ed25519.PublicKeySize {
		t.Fatalf("ed25519 公钥 = %q (%v), want %d 字节的 base64", info.Ed25519, err, ed25519.PublicKeySize)
	}
	if want := identityFingerprint(edPub); info.Fingerprint != want {
		t.Fatalf("指纹 = %q, want sha256(公钥) = %q", info.Fingerprint, want)
	}
	ecdhPub, err := base64.StdEncoding.DecodeString(info.ECDH)
	if err != nil || len(ecdhPub) != 32 {
		t.Fatalf("ecdh 公钥 = %q (%v), want 32 字节的 base64", info.ECDH, err)
	}

	// 私钥落盘在配置目录下，重启后还能读回同一套密钥。
	local, err := loadLocalIdentity()
	if err != nil || local == nil {
		t.Fatalf("loadLocalIdentity = %v, %v; want the identity Setup just made", local, err)
	}
	if local.fingerprint() != info.Fingerprint {
		t.Fatalf("重载的指纹 = %q, want %q", local.fingerprint(), info.Fingerprint)
	}
	if _, err := os.Stat(filepath.Join(mustStoreDir(t), identityFileName)); err != nil {
		t.Fatalf("identity.json 没写出来：%v", err)
	}
}

func mustStoreDir(t *testing.T) string {
	t.Helper()
	dir, err := identityStoreDir()
	if err != nil {
		t.Fatalf("identityStoreDir failed: %v", err)
	}
	return dir
}

// 再调用 Setup 不重建密钥：换密钥等于换身份，旧签名与加密文件都会失效。
func TestIdentitySetupKeepsExistingKeys(t *testing.T) {
	useTempConfig(t)
	service := IdentityService{}

	first, err := service.Setup("first@example.com")
	if err != nil {
		t.Fatalf("第一次 Setup failed: %v", err)
	}
	second, err := service.Setup("second@example.com")
	if err != nil {
		t.Fatalf("第二次 Setup failed: %v", err)
	}
	if second.Fingerprint != first.Fingerprint || second.Ed25519 != first.Ed25519 || second.ECDH != first.ECDH {
		t.Fatalf("第二次 Setup 换了密钥：%#v -> %#v", first, second)
	}
	if second.Email != "first@example.com" {
		t.Fatalf("email = %q, want the first one kept", second.Email)
	}
}

// Update 只改展示信息：指纹与密钥不变，改完重启也是新的展示信息。
func TestIdentityUpdateKeepsKeys(t *testing.T) {
	useTempConfig(t)
	service := IdentityService{}
	before, err := service.Setup("old@example.com")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	after, err := service.Update("new@example.com", "新名字")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if after.Email != "new@example.com" || after.ID != "新名字" {
		t.Fatalf("Update 之后的展示信息 = %q/%q", after.ID, after.Email)
	}
	if after.Fingerprint != before.Fingerprint || after.Ed25519 != before.Ed25519 || after.ECDH != before.ECDH {
		t.Fatalf("Update 改了密钥或指纹：%#v -> %#v", before, after)
	}

	local, err := loadLocalIdentity()
	if err != nil || local == nil {
		t.Fatalf("loadLocalIdentity = %v, %v", local, err)
	}
	if local.ID != "新名字" || local.Email != "new@example.com" {
		t.Fatalf("重启后读到 %q/%q, want 新名字/new@example.com", local.ID, local.Email)
	}
	if local.fingerprint() != before.Fingerprint {
		t.Fatalf("重启后的指纹 = %q, want %q", local.fingerprint(), before.Fingerprint)
	}

	// 空串表示这一项不动。
	same, err := service.Update("", "")
	if err != nil {
		t.Fatalf("Update with empty strings failed: %v", err)
	}
	if same.Email != "new@example.com" || same.ID != "新名字" {
		t.Fatalf("空串 Update 改掉了展示信息：%q/%q", same.ID, same.Email)
	}
}

// 信任列表：按指纹去重，Untrust 移除后列表为空。
func TestTrustedListRoundTrip(t *testing.T) {
	useTempConfig(t)
	service := IdentityService{}
	other := newTestIdentity(t, "other@example.com")

	row := other.bandIdentity().trustedRow()
	if err := addTrustedIdentity(row); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	if err := addTrustedIdentity(row); err != nil {
		t.Fatalf("重复 addTrustedIdentity failed: %v", err)
	}
	trusted := service.Trusted()
	if len(trusted) != 1 {
		t.Fatalf("trusted = %#v, want one row", trusted)
	}
	if trusted[0].Fingerprint != other.fingerprint() || trusted[0].ECDH == "" || trusted[0].Ed25519 == "" {
		t.Fatalf("trusted row = %#v, want the other identity's public keys", trusted[0])
	}

	service.Untrust(other.fingerprint())
	if left := service.Trusted(); len(left) != 0 {
		t.Fatalf("Untrust 之后 trusted = %#v, want empty", left)
	}
}

// 身份缓存按指纹去重、上限 200 条，超出丢最旧。
func TestIdentityCacheLimitAndDedupe(t *testing.T) {
	useTempConfig(t)

	first := newTestIdentity(t, "first@example.com")
	if err := cacheIdentity(first.bandIdentity().cached()); err != nil {
		t.Fatalf("cacheIdentity failed: %v", err)
	}
	if err := cacheIdentity(first.bandIdentity().cached()); err != nil {
		t.Fatalf("重复 cacheIdentity failed: %v", err)
	}
	if cached := readCachedIdentities(); len(cached) != 1 {
		t.Fatalf("cached = %#v, want one row after a duplicate", cached)
	}

	for n := 0; n < identityCacheLimit+5; n++ {
		identity := newTestIdentity(t, "spam@example.com")
		if err := cacheIdentity(identity.bandIdentity().cached()); err != nil {
			t.Fatalf("cacheIdentity failed: %v", err)
		}
	}
	cached := readCachedIdentities()
	if len(cached) != identityCacheLimit {
		t.Fatalf("cached = %d rows, want the %d cap", len(cached), identityCacheLimit)
	}
	for _, row := range cached {
		if row.Fingerprint == first.fingerprint() {
			t.Fatalf("最旧的一条还在缓存里")
		}
	}
}

// 收件人候选 = 自己 + 持久信任 + 身份缓存，按指纹去重。
func TestCandidatesCombineSources(t *testing.T) {
	useTempConfig(t)
	service := IdentityService{}
	if _, err := service.Setup("me@example.com"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	trusted := newTestIdentity(t, "trusted@example.com")
	if err := addTrustedIdentity(trusted.bandIdentity().trustedRow()); err != nil {
		t.Fatalf("addTrustedIdentity failed: %v", err)
	}
	cached := newTestIdentity(t, "cached@example.com")
	if err := cacheIdentity(cached.bandIdentity().cached()); err != nil {
		t.Fatalf("cacheIdentity failed: %v", err)
	}

	candidates := service.Candidates()
	fingerprints := map[string]bool{}
	for _, candidate := range candidates {
		fingerprints[candidate.Fingerprint] = true
	}
	local, err := loadLocalIdentity()
	if err != nil || local == nil {
		t.Fatalf("loadLocalIdentity = %v, %v", local, err)
	}
	for _, want := range []string{local.fingerprint(), trusted.fingerprint(), cached.fingerprint()} {
		if !fingerprints[want] {
			t.Fatalf("候选里没有 %q：%#v", want, candidates)
		}
	}
	if len(candidates) != 3 {
		t.Fatalf("candidates = %#v, want 3 rows", candidates)
	}
}
