package ndapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"hash"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// newTestRuntime builds the runtime shape a real script sees: a fresh goja
// runtime plus the nd.crypto and nd.time objects NewRun injects.
func newTestRuntime(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	run := NewRun(vm, nil, nil)
	nd := vm.NewObject()
	_ = nd.Set("crypto", run.CryptoGlobal())
	_ = nd.Set("time", run.TimeGlobal())
	_ = vm.Set("nd", nd)
	return vm
}

// evalScript runs src with globals injected and returns its completion value as
// text.
func evalScript(t *testing.T, globals map[string]any, src string) string {
	t.Helper()
	vm := newTestRuntime(t)
	for key, value := range globals {
		if err := vm.Set(key, value); err != nil {
			t.Fatalf("set global %s: %v", key, err)
		}
	}
	value, err := vm.RunString(src)
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	return value.String()
}

// splitParts splits a "|"-joined result set, so one script run can report
// several values.
func splitParts(out string) []string { return strings.Split(out, "|") }

func TestDigests(t *testing.T) {
	out := evalScript(t, nil, `[
		nd.crypto.md5("abc"),
		nd.crypto.sha1("abc"),
		nd.crypto.sha224("abc"),
		nd.crypto.sha256("abc"),
		nd.crypto.sha384("abc"),
		nd.crypto.sha512("abc")
	].join("|")`)

	want := []string{
		"900150983cd24fb0d6963f7d28e17f72",
		"a9993e364706816aba3e25717850c26c9cd0d89d",
		"23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7",
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7",
		"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
	}
	got := splitParts(out)
	if len(got) != len(want) {
		t.Fatalf("got %d digests, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("digest %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHMAC(t *testing.T) {
	const key, data = "Jefe", "what do ya want for nothing?"
	out := evalScript(t, nil, `[
		nd.crypto.hmac("md5", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("sha1", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("sha224", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("sha256", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("sha384", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("sha512", "Jefe", "what do ya want for nothing?"),
		nd.crypto.hmac("SHA256", "Jefe", "what do ya want for nothing?")
	].join("|")`)

	// RFC 2202 test case 2 (md5/sha1) and RFC 4231 test case 2 (sha*): they pin
	// the new key-before-data argument order.
	hashByName := []func() hash.Hash{md5.New, sha1.New, sha256.New224, sha256.New, sha512.New384, sha512.New, sha256.New}
	got := splitParts(out)
	if len(got) != len(hashByName) {
		t.Fatalf("got %d hmacs, want %d", len(got), len(hashByName))
	}
	for i, newHash := range hashByName {
		mac := hmac.New(newHash, []byte(key))
		_, _ = mac.Write([]byte(data))
		if want := hex.EncodeToString(mac.Sum(nil)); got[i] != want {
			t.Errorf("hmac %d = %q, want %q", i, got[i], want)
		}
	}
	if got[0] != "750c783e6ab0b503eaa86e310a5db738" {
		t.Errorf("hmac md5 = %q, want the RFC 2202 vector", got[0])
	}
	if got[3] != "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" {
		t.Errorf("hmac sha256 = %q, want the RFC 4231 vector", got[3])
	}
	if got[6] != got[3] {
		t.Errorf("algo names must be case-insensitive: %q != %q", got[6], got[3])
	}
}

func TestAES(t *testing.T) {
	out := evalScript(t, nil, `
	var key = "hex:2b7e151628aed2a6abf7158809cf4f3c";
	var iv = "hex:000102030405060708090a0b0c0d0e0f";
	var pt = nd.crypto.hexDecode("6bc1bee22e409f96e93d7e117393172a");
	var cbc = nd.crypto.aesEncrypt(pt, key, iv);
	var ecb = nd.crypto.aesEncrypt(pt, key, "", "ecb");
	var cbcBack = nd.crypto.aesDecrypt(cbc, key, iv);
	var ecbBack = nd.crypto.aesDecrypt(ecb, key, "", "ecb");
	[
		cbc,
		ecb,
		cbcBack === pt ? "cbc-ok" : "cbc-bad",
		ecbBack === pt ? "ecb-ok" : "ecb-bad",
		nd.crypto.hexEncode(cbcBack)
	].join("|")`)

	parts := splitParts(out)
	// NIST SP 800-38A F.2.1 / F.1.1 first blocks. The plaintext is
	// block-aligned, so PKCS#7 appends a full block: compare the prefix.
	cbc, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode cbc: %v", err)
	}
	if got := hex.EncodeToString(cbc[:16]); got != "7649abac8119b246cee98e9b12e9197d" {
		t.Errorf("CBC first block = %q, want the NIST vector", got)
	}
	ecb, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode ecb: %v", err)
	}
	if got := hex.EncodeToString(ecb[:16]); got != "3ad77bb40d7a3660a89ecaf32466ef97" {
		t.Errorf("ECB first block = %q, want the NIST vector", got)
	}
	if got, want := parts[2], "cbc-ok"; got != want {
		t.Errorf("CBC round-trip = %q, want %q", got, want)
	}
	if got, want := parts[3], "ecb-ok"; got != want {
		t.Errorf("ECB round-trip = %q, want %q", got, want)
	}
	if got, want := parts[4], "6bc1bee22e409f96e93d7e117393172a"; got != want {
		t.Errorf("CBC plaintext bytes = %q, want %q", got, want)
	}
}

func TestAESGCM(t *testing.T) {
	out := evalScript(t, nil, `
	var key = "hex:00000000000000000000000000000000";
	var nonce = "hex:000000000000000000000000";
	var empty = nd.crypto.aesEncrypt("", key, nonce, "gcm");
	var round = nd.crypto.aesEncrypt("hello gcm", key, "", "gcm");
	var explicit = nd.crypto.aesEncrypt("explicit nonce", key, nonce, "gcm");
	var malformed = nd.crypto.base64Encode("123456789012");
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		empty,
		nd.crypto.aesDecrypt(empty, key, "", "gcm") === "" ? "empty-ok" : "empty-bad",
		nd.crypto.aesDecrypt(round, key, "", "gcm"),
		nd.crypto.aesDecrypt(explicit, key, nonce, "gcm"),
		nd.crypto.aesDecrypt(explicit, key, "", "gcm"),
		msg(function () { nd.crypto.aesDecrypt(malformed, key, "", "gcm"); })
	].join("|")`)

	parts := splitParts(out)
	// NIST GCM test case 1 tag (AES-128, zero key and nonce, empty payload).
	// The new output format is base64(nonce || ciphertext || tag).
	raw, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode gcm: %v", err)
	}
	if len(raw) != 12+16 {
		t.Fatalf("gcm output is %d bytes, want nonce(12)+tag(16)", len(raw))
	}
	if got, want := hex.EncodeToString(raw[:12]), "000000000000000000000000"; got != want {
		t.Errorf("gcm nonce prefix = %q, want %q", got, want)
	}
	if got, want := hex.EncodeToString(raw[12:]), "58e2fccefa7e3061367f1d57a4e7455a"; got != want {
		t.Errorf("gcm tag = %q, want %q", got, want)
	}
	if got, want := parts[1], "empty-ok"; got != want {
		t.Errorf("empty payload decrypt = %q, want %q", got, want)
	}
	if got, want := parts[2], "hello gcm"; got != want {
		t.Errorf("gcm round-trip = %q, want %q", got, want)
	}
	if got, want := parts[3], "explicit nonce"; got != want {
		t.Errorf("gcm explicit-nonce round-trip = %q, want %q", got, want)
	}
	if got, want := parts[4], "explicit nonce"; got != want {
		t.Errorf("gcm decrypt without iv = %q, want %q", got, want)
	}
	if !strings.Contains(parts[5], "解密失败") {
		t.Errorf("malformed gcm ciphertext must throw a decrypt error, got %q", parts[5])
	}
}

func TestDES(t *testing.T) {
	out := evalScript(t, nil, `
	var desKey = "hex:0123456789abcdef";
	var ecb = nd.crypto.desEncrypt("Now is t", desKey, "", "ecb");
	var key16 = "hex:0123456789abcdeffedcba9876543210";
	var key24 = "hex:0123456789abcdeffedcba98765432100011223344556677";
	var iv = "hex:0102030405060708";
	var cbc16 = nd.crypto.desEncrypt("hello triple-des", key16, iv);
	var cbc24 = nd.crypto.desEncrypt("hello triple-des", key24, iv);
	// NIST "Block Cipher Modes of Operation" CBC-TDES example: three distinct
	// subkeys and a block-aligned 32-byte plaintext.
	var nistKey = "hex:0123456789abcdef23456789abcdef01456789abcdef0123";
	var nistIv = "hex:f69f2445df4f9b17";
	var nistPt = nd.crypto.hexDecode("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51");
	var nistCt = nd.crypto.desEncrypt(nistPt, nistKey, nistIv);
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		ecb,
		nd.crypto.desDecrypt(ecb, desKey, "", "ecb"),
		nd.crypto.desDecrypt(cbc16, key16, iv),
		nd.crypto.desDecrypt(cbc24, key24, iv),
		nistCt,
		nd.crypto.hexEncode(nd.crypto.desDecrypt(nistCt, nistKey, nistIv)),
		msg(function () { nd.crypto.desEncrypt("x", desKey, "", "gcm"); })
	].join("|")`)

	parts := splitParts(out)
	// The classic OpenSSL des-ecb textbook vector; the plaintext is one block,
	// so a full PKCS#7 padding block follows.
	raw, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode des: %v", err)
	}
	if got := hex.EncodeToString(raw[:8]); got != "3fa40e8a984d4815" {
		t.Errorf("DES ECB first block = %q, want the textbook vector", got)
	}
	if got, want := parts[1], "Now is t"; got != want {
		t.Errorf("DES round-trip = %q, want %q", got, want)
	}
	if got, want := parts[2], "hello triple-des"; got != want {
		t.Errorf("3DES-16 round-trip = %q, want %q", got, want)
	}
	if got, want := parts[3], "hello triple-des"; got != want {
		t.Errorf("3DES-24 round-trip = %q, want %q", got, want)
	}
	nist, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		t.Fatalf("decode 3DES CBC: %v", err)
	}
	// The NIST example is unpadded; this API appends PKCS#7, so the ciphertext
	// is one 8-byte block longer and only the first 32 bytes match the vector.
	if len(nist) != 40 {
		t.Fatalf("3DES CBC ciphertext is %d bytes, want 32+8 padding", len(nist))
	}
	wantNIST := "2079c3d53aa763e193b79e2569ab5262516570481f25b50f73c0bda85c8e0da7"
	if got := hex.EncodeToString(nist[:32]); got != wantNIST {
		t.Errorf("3DES CBC ciphertext = %q, want the NIST vector %q", got, wantNIST)
	}
	if got, want := parts[5], "6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51"; got != want {
		t.Errorf("3DES CBC round-trip = %q, want %q", got, want)
	}
	if !strings.Contains(parts[6], "不支持 gcm") {
		t.Errorf("DES must reject gcm, got %q", parts[6])
	}
}

// testRSAKeys generates the keys the RSA and JWT tests share: one usable
// keypair, a second one for wrong-key checks, and an EC key for the "not RSA"
// checks.
func testRSAKeys(t *testing.T) (priv, pub, priv8, otherPub, ec8 string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	priv = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	priv8DER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS#8: %v", err)
	}
	priv8 = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv8DER}))

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate second RSA key: %v", err)
	}
	otherDER, err := x509.MarshalPKIXPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatalf("marshal second public key: %v", err)
	}
	otherPub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: otherDER}))

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatalf("marshal EC PKCS#8: %v", err)
	}
	ec8 = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}))
	return priv, pub, priv8, otherPub, ec8
}

func TestRSA(t *testing.T) {
	priv, pub, priv8, otherPub, ec8 := testRSAKeys(t)
	out := evalScript(t, map[string]any{
		"PRIV": priv, "PUB": pub, "PRIV8": priv8, "OTHER": otherPub, "EC8": ec8,
	}, `
	var sig = nd.crypto.rsaSign("payload", PRIV);
	var psig = nd.crypto.rsaPssSign("payload", PRIV);
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		nd.crypto.rsaVerify("payload", sig, PUB) ? "ok" : "bad",
		nd.crypto.rsaVerify("tampered", sig, PUB) ? "ok" : "bad",
		nd.crypto.rsaVerify("payload", sig, OTHER) ? "ok" : "bad",
		nd.crypto.rsaVerify("payload", nd.crypto.rsaSign("payload", PRIV, "sha384"), PUB, "sha384") ? "ok" : "bad",
		nd.crypto.rsaVerify("payload", nd.crypto.rsaSign("payload", PRIV8), PUB) ? "ok" : "bad",
		nd.crypto.rsaPssVerify("payload", psig, PUB) ? "ok" : "bad",
		nd.crypto.rsaPssVerify("tampered", psig, PUB) ? "ok" : "bad",
		nd.crypto.rsaDecrypt(nd.crypto.rsaEncrypt("top secret", PUB), PRIV),
		msg(function () { nd.crypto.rsaSign("x", PRIV, "md5"); }),
		msg(function () { nd.crypto.rsaSign("x", EC8); }),
		msg(function () { nd.crypto.rsaSign("x", "not pem"); })
	].join("|")`)

	want := []string{"ok", "bad", "bad", "ok", "ok", "ok", "bad", "top secret"}
	parts := splitParts(out)
	for i, value := range want {
		if parts[i] != value {
			t.Errorf("rsa result %d = %q, want %q", i, parts[i], value)
		}
	}
	if !strings.Contains(parts[8], "md5") {
		t.Errorf("rsaSign(md5) must be rejected, got %q", parts[8])
	}
	if !strings.Contains(parts[9], "不是 RSA") {
		t.Errorf("a non-RSA PKCS#8 key must be rejected, got %q", parts[9])
	}
	if !strings.Contains(parts[10], "不是 PEM") {
		t.Errorf("a non-PEM key must be rejected, got %q", parts[10])
	}
}

func TestPBKDF2(t *testing.T) {
	out := evalScript(t, nil, `[
		nd.crypto.pbkdf2("password", "salt", 1, 20, "sha1"),
		nd.crypto.pbkdf2("password", "salt", 4096, 20, "sha1"),
		nd.crypto.pbkdf2("password", "salt", 1, 32, "sha256")
	].join("|")`)

	// RFC 6070 (SHA-1) and the widely published PBKDF2-HMAC-SHA256 vector.
	want := []string{
		"0c60c80f961f0e71f3a9b524af6012062fe037a6",
		"4b007901b765489abead49d926f721d065a429c1",
		"120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b",
	}
	parts := splitParts(out)
	for i, value := range want {
		raw, err := base64.StdEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("decode pbkdf2 result %d: %v", i, err)
		}
		if got := hex.EncodeToString(raw); got != value {
			t.Errorf("pbkdf2 %d = %q, want %q", i, got, value)
		}
	}
}

func TestJWT(t *testing.T) {
	priv, pub, _, _, _ := testRSAKeys(t)
	out := evalScript(t, map[string]any{"PRIV": priv, "PUB": pub}, `
	var token = nd.crypto.jwtSign({ sub: "123", n: 1 }, "secret", "HS256");
	var payload = nd.crypto.jwtVerify(token, "secret");
	var rs = nd.crypto.jwtSign({ a: 1 }, PRIV, "RS256");
	var parts = rs.split(".");
	var tampered = parts[0] + "." + parts[1] + "." + parts[2].slice(0, -4) + "AAAA";
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		String(token.split(".").length),
		payload.sub + ":" + payload.n,
		nd.crypto.jwtVerify(rs, PUB, "RS256").a === 1 ? "ok" : "bad",
		msg(function () { nd.crypto.jwtVerify(token, "wrong"); }),
		msg(function () { nd.crypto.jwtVerify(token, "secret", "HS384"); }),
		msg(function () { nd.crypto.jwtVerify(rs, "secret", "HS256"); }),
		msg(function () { nd.crypto.jwtVerify(tampered, PUB, "RS256"); }),
		msg(function () { nd.crypto.jwtVerify("only.two", "secret"); })
	].join("|")`)

	parts := splitParts(out)
	want := map[int]string{0: "3", 1: "123:1", 2: "ok"}
	for i, value := range want {
		if parts[i] != value {
			t.Errorf("jwt result %d = %q, want %q", i, parts[i], value)
		}
	}
	// Wrong secret, algo mismatch, alg downgrade, a tampered signature and a
	// malformed token all throw.
	for i := 3; i <= 7; i++ {
		if parts[i] == "no-throw" || parts[i] == "" {
			t.Errorf("jwt error case %d did not throw: %q", i, parts[i])
		}
	}
	if !strings.Contains(parts[5], "不一致") {
		t.Errorf("alg downgrade must name the mismatch, got %q", parts[5])
	}
}

func TestURLCodec(t *testing.T) {
	out := evalScript(t, nil, `[
		nd.crypto.urlEncode("a b+c/中"),
		nd.crypto.urlDecode(nd.crypto.urlEncode("a b+c/中")),
		nd.crypto.urlDecode("a+b"),
		nd.crypto.urlEncode("-._~")
	].join("|")`)

	want := []string{"a%20b%2Bc%2F%E4%B8%AD", "a b+c/中", "a+b", "-._~"}
	parts := splitParts(out)
	for i, value := range want {
		if parts[i] != value {
			t.Errorf("url result %d = %q, want %q", i, parts[i], value)
		}
	}
}

func TestBytesCodec(t *testing.T) {
	out := evalScript(t, nil, `
	var raw = nd.crypto.bytesFromBase64("gIGC");
	var text = nd.crypto.bytesFromBase64(nd.crypto.base64Encode("中文"));
	var dead = nd.crypto.bytesFromHex("dead");
	var same = nd.crypto.bytesFromBase64("3q0=");
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		raw instanceof Uint8Array && raw.length === 3 && raw[0] === 128 && raw[2] === 130 ? "ok" : "bad",
		nd.crypto.bytesToBase64(raw),
		nd.crypto.bytesToBase64(text),
		nd.crypto.bytesToBase64(new Uint8Array([1, 2, 3]).buffer),
		nd.crypto.bytesToBase64(new Uint8Array([1, 2, 3])),
		nd.crypto.bytesToHex(new Uint8Array([0xde, 0xad])),
		dead instanceof Uint8Array && dead.length === 2 && dead[0] === 0xde && dead[1] === 0xad ? "ok" : "bad",
		nd.crypto.bytesToHex(dead),
		nd.crypto.bytesToHex(raw),
		nd.crypto.bytesToHex(same),
		nd.crypto.bytesToBase64(same),
		msg(function () { nd.crypto.bytesFromHex("中文/非法"); }),
		msg(function () { nd.crypto.bytesToHex("text"); })
	].join("|")`)

	want := []string{
		"ok",
		"gIGC",
		base64.StdEncoding.EncodeToString([]byte("中文")),
		"AQID",
		"AQID",
		"dead",
		"ok",
		"dead",
		"808182",
		"dead",
		"3q0=",
	}
	parts := splitParts(out)
	for i, value := range want {
		if parts[i] != value {
			t.Errorf("bytes result %d = %q, want %q", i, parts[i], value)
		}
	}
	if !strings.Contains(parts[11], "hex 解码失败") {
		t.Errorf("invalid hex must throw, got %q", parts[11])
	}
	if !strings.Contains(parts[12], "必须是 Uint8Array 或 ArrayBuffer") {
		t.Errorf("a non-byte argument must be rejected, got %q", parts[12])
	}
}

func TestKeyPrefixes(t *testing.T) {
	keyB64 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	out := evalScript(t, map[string]any{"KEYB64": keyB64}, `
	// Hex of the ASCII key "0123456789abcdef", so hex:/raw:/base64 spellings
	// must select the same 16 bytes.
	var hexKey = "hex:30313233343536373839616263646566";
	var b64Key = "b64:" + KEYB64;
	function enc(key) { return nd.crypto.aesEncrypt("hello", key, "", "ecb"); }
	var a = enc(hexKey);
	[
		a === enc(b64Key) && a === enc(KEYB64) ? "ok" : "bad",
		enc(hexKey) === enc("raw:0123456789abcdef") ? "ok" : "bad",
		nd.crypto.aesDecrypt(a, KEYB64, "", "ecb")
	].join("|")`)

	parts := splitParts(out)
	if parts[0] != "ok" || parts[1] != "ok" || parts[2] != "hello" {
		t.Errorf("key prefixes disagree: %v", parts)
	}
}

func TestCryptoErrors(t *testing.T) {
	out := evalScript(t, nil, `
	function msg(f) {
		try { f(); return "no-throw"; } catch (e) { return e.message; }
	}
	[
		msg(function () { nd.crypto.md5(); }),
		msg(function () { nd.crypto.hmac("sha999", "k", "d"); }),
		msg(function () { nd.crypto.base64Decode("!bad!"); }),
		msg(function () { nd.crypto.aesEncrypt("d", "a2V5", "", "xyz"); }),
		msg(function () { nd.crypto.aesEncrypt("d", "a2V5"); }),
		msg(function () { nd.crypto.aesEncrypt("d", "hex:zz"); }),
		msg(function () { nd.crypto.aesEncrypt("d", "a2V5", "", "ecb"); }),
		msg(function () { nd.crypto.pbkdf2("p", "s", 0, 16); }),
		msg(function () { nd.crypto.pbkdf2("p", "s", 1, 99999); }),
		msg(function () { nd.crypto.bytesToBase64("text"); }),
		msg(function () { nd.crypto.bytesFromBase64("!bad!"); }),
		msg(function () { nd.crypto.urlDecode("%zz"); })
	].join("|")`)

	contains := []string{
		"缺少参数 data",
		"不支持的 algo",
		"base64 解码失败",
		"不支持的 mode",
		"key 必须是 16 / 24 / 32 字节",
		"不是合法 hex",
		"key 必须是 16 / 24 / 32 字节",
		"iterations 必须在 1 到",
		"keyLen 必须在 1 到",
		"必须是 Uint8Array 或 ArrayBuffer",
		"base64 解码失败",
		"百分号编码不合法",
	}
	parts := splitParts(out)
	for i, want := range contains {
		if !strings.Contains(parts[i], want) {
			t.Errorf("error %d = %q, want it to contain %q", i, parts[i], want)
		}
	}
}
