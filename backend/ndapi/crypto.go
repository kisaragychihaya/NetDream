package ndapi

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"hash"
	"net/url"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// cryptoMaxRandom caps the byte count nd.crypto.randomBytes/randomHex and the
// PBKDF2 output length accept, so a typo cannot ask for an unbounded
// allocation.
const cryptoMaxRandom = 4096

// cryptoMaxPBKDF2Iterations caps the PBKDF2 iteration count; the run timeout
// bounds a long derivation anyway, and legitimate API-signing use stays far
// below this.
const cryptoMaxPBKDF2Iterations = 10_000_000

// CryptoGlobal is nd.crypto: digests, HMAC, symmetric ciphers (AES, DES/3DES),
// RSA signatures and encryption, PBKDF2, JWT and the text codecs. Every argument
// and every result is a string: nd.vars only carries text, so byte slices never
// cross into the script. Byte-level helpers (bytesToBase64/bytesFromBase64 and
// bytesToHex/bytesFromHex) are the one exception and take or return a
// Uint8Array.
func (run *Run) CryptoGlobal() *goja.Object {
	vm := run.vm
	api := vm.NewObject()

	digest := func(method string, newHash func() hash.Hash) {
		name := "nd.crypto." + method
		_ = api.Set(method, func(call goja.FunctionCall) goja.Value {
			data := run.cryptoString(name, call, 0, "data")
			encoding := run.cryptoEncoding(name, call, 1)
			sum := newHash()
			_, _ = sum.Write([]byte(data))
			return vm.ToValue(cryptoEncode(encoding, sum.Sum(nil)))
		})
	}
	digest("md5", md5.New)
	digest("sha1", sha1.New)
	digest("sha224", sha256.New224)
	digest("sha256", sha256.New)
	digest("sha384", sha512.New384)
	digest("sha512", sha512.New)

	_ = api.Set("hmac", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.hmac"
		algo := run.cryptoString(name, call, 0, "algo")
		key := run.cryptoString(name, call, 1, "key")
		data := run.cryptoString(name, call, 2, "data")
		encoding := run.cryptoEncoding(name, call, 3)

		mac := hmac.New(run.cryptoHash(name, algo), []byte(key))
		_, _ = mac.Write([]byte(data))
		return vm.ToValue(cryptoEncode(encoding, mac.Sum(nil)))
	})

	_ = api.Set("base64Encode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.base64Encode"
		data := run.cryptoString(name, call, 0, "data")
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(data)))
	})
	_ = api.Set("base64Decode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.base64Decode"
		data := run.cryptoString(name, call, 0, "data")
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			panic(run.newError(name + "(): base64 解码失败：" + err.Error()))
		}
		return vm.ToValue(string(raw))
	})
	_ = api.Set("base64UrlEncode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.base64UrlEncode"
		data := run.cryptoString(name, call, 0, "data")
		return vm.ToValue(base64.RawURLEncoding.EncodeToString([]byte(data)))
	})
	_ = api.Set("base64UrlDecode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.base64UrlDecode"
		data := run.cryptoString(name, call, 0, "data")
		raw, err := cryptoDecodeBase64URL(data)
		if err != nil {
			panic(run.newError(name + "(): base64url 解码失败：" + err.Error()))
		}
		return vm.ToValue(string(raw))
	})
	_ = api.Set("hexEncode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.hexEncode"
		data := run.cryptoString(name, call, 0, "data")
		return vm.ToValue(hex.EncodeToString([]byte(data)))
	})
	_ = api.Set("hexDecode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.hexDecode"
		data := run.cryptoString(name, call, 0, "data")
		raw, err := hex.DecodeString(data)
		if err != nil {
			panic(run.newError(name + "(): hex 解码失败：" + err.Error()))
		}
		return vm.ToValue(string(raw))
	})
	_ = api.Set("urlEncode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.urlEncode"
		data := run.cryptoString(name, call, 0, "data")
		return vm.ToValue(cryptoEncodeURIComponent(data))
	})
	_ = api.Set("urlDecode", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.urlDecode"
		data := run.cryptoString(name, call, 0, "data")
		text, err := url.PathUnescape(data)
		if err != nil {
			panic(run.newError(name + "(): 百分号编码不合法：" + err.Error()))
		}
		return vm.ToValue(text)
	})

	_ = api.Set("aesEncrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.aesEncrypt"
		data, key, iv, mode := run.aesArgs(name, call)
		block := run.aesBlock(name, key)

		if mode == "gcm" {
			gcm := run.aesGCM(name, block)
			nonce := iv
			if len(nonce) == 0 {
				nonce = make([]byte, gcm.NonceSize())
				if _, err := rand.Read(nonce); err != nil {
					panic(run.newError(name + "(): " + err.Error()))
				}
			} else {
				nonce = run.aesGCMNonce(name, nonce, gcm.NonceSize())
			}
			sealed := gcm.Seal(nil, nonce, []byte(data), nil)
			out := make([]byte, 0, len(nonce)+len(sealed))
			out = append(out, nonce...)
			out = append(out, sealed...)
			return vm.ToValue(base64.StdEncoding.EncodeToString(out))
		}

		out := run.blockEncrypt(name, block, iv, mode, []byte(data))
		return vm.ToValue(base64.StdEncoding.EncodeToString(out))
	})
	_ = api.Set("aesDecrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.aesDecrypt"
		data, key, iv, mode := run.aesArgs(name, call)
		block := run.aesBlock(name, key)

		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			panic(run.newError(name + "(): 参数 data 不是合法 base64：" + err.Error()))
		}

		if mode == "gcm" {
			gcm := run.aesGCM(name, block)
			size := gcm.NonceSize()
			nonce, payload := iv, raw
			if len(nonce) == 0 {
				if len(payload) < size {
					panic(run.newError(name + "(): GCM 密文 " + strconv.Itoa(len(payload)) + " 字节不足 " + strconv.Itoa(size) + " 字节 nonce"))
				}
				nonce, payload = payload[:size], payload[size:]
			} else {
				nonce = run.aesGCMNonce(name, nonce, size)
			}
			plain, err := gcm.Open(nil, nonce, payload, nil)
			if err != nil && len(iv) > 0 && len(payload) > size && bytes.HasPrefix(payload, nonce) {
				// aesEncrypt always prefixes the nonce, even one the script gave
				// explicitly; strip it so that round-trip works. The first
				// attempt still covers ciphertexts written without the prefix.
				plain, err = gcm.Open(nil, nonce, payload[size:], nil)
			}
			if err != nil {
				panic(run.newError(name + "(): 解密失败，key / iv 或密文不匹配"))
			}
			return vm.ToValue(string(plain))
		}

		return vm.ToValue(string(run.blockDecrypt(name, block, iv, mode, raw)))
	})

	_ = api.Set("desEncrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.desEncrypt"
		data, key, iv, mode := run.aesArgs(name, call)
		if mode == "gcm" {
			panic(run.newError(name + "(): DES / 3DES 不支持 gcm"))
		}
		block := run.desBlock(name, key)
		out := run.blockEncrypt(name, block, iv, mode, []byte(data))
		return vm.ToValue(base64.StdEncoding.EncodeToString(out))
	})
	_ = api.Set("desDecrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.desDecrypt"
		data, key, iv, mode := run.aesArgs(name, call)
		if mode == "gcm" {
			panic(run.newError(name + "(): DES / 3DES 不支持 gcm"))
		}
		block := run.desBlock(name, key)
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			panic(run.newError(name + "(): 参数 data 不是合法 base64：" + err.Error()))
		}
		return vm.ToValue(string(run.blockDecrypt(name, block, iv, mode, raw)))
	})

	_ = api.Set("rsaSign", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaSign"
		data := run.cryptoString(name, call, 0, "data")
		key := run.rsaPrivateKey(name, run.cryptoString(name, call, 1, "privateKey"))
		ch, newHash := run.cryptoRSAHash(name, call, 2)
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, ch, cryptoDigest(newHash, data))
		if err != nil {
			panic(run.newError(name + "(): " + err.Error()))
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(sig))
	})
	_ = api.Set("rsaVerify", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaVerify"
		data := run.cryptoString(name, call, 0, "data")
		sig := run.cryptoBase64(name, "signature", run.cryptoString(name, call, 1, "signature"))
		pub := run.rsaPublicKey(name, run.cryptoString(name, call, 2, "publicKey"))
		ch, newHash := run.cryptoRSAHash(name, call, 3)
		err := rsa.VerifyPKCS1v15(pub, ch, cryptoDigest(newHash, data), sig)
		return vm.ToValue(err == nil)
	})
	_ = api.Set("rsaPssSign", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaPssSign"
		data := run.cryptoString(name, call, 0, "data")
		key := run.rsaPrivateKey(name, run.cryptoString(name, call, 1, "privateKey"))
		ch, newHash := run.cryptoRSAHash(name, call, 2)
		sig, err := rsa.SignPSS(rand.Reader, key, ch, cryptoDigest(newHash, data), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			panic(run.newError(name + "(): " + err.Error()))
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(sig))
	})
	_ = api.Set("rsaPssVerify", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaPssVerify"
		data := run.cryptoString(name, call, 0, "data")
		sig := run.cryptoBase64(name, "signature", run.cryptoString(name, call, 1, "signature"))
		pub := run.rsaPublicKey(name, run.cryptoString(name, call, 2, "publicKey"))
		ch, newHash := run.cryptoRSAHash(name, call, 3)
		err := rsa.VerifyPSS(pub, ch, cryptoDigest(newHash, data), sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		return vm.ToValue(err == nil)
	})
	_ = api.Set("rsaEncrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaEncrypt"
		data := run.cryptoString(name, call, 0, "data")
		pub := run.rsaPublicKey(name, run.cryptoString(name, call, 1, "publicKey"))
		out, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(data))
		if err != nil {
			panic(run.newError(name + "(): " + err.Error()))
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(out))
	})
	_ = api.Set("rsaDecrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.rsaDecrypt"
		data := run.cryptoString(name, call, 0, "data")
		key := run.rsaPrivateKey(name, run.cryptoString(name, call, 1, "privateKey"))
		raw := run.cryptoBase64(name, "data", data)
		out, err := rsa.DecryptPKCS1v15(rand.Reader, key, raw)
		if err != nil {
			panic(run.newError(name + "(): " + err.Error()))
		}
		return vm.ToValue(string(out))
	})

	_ = api.Set("pbkdf2", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.pbkdf2"
		password := run.cryptoString(name, call, 0, "password")
		salt := run.cryptoString(name, call, 1, "salt")
		iterations := run.cryptoNumber(name, call, 2, "iterations")
		keyLen := run.cryptoNumber(name, call, 3, "keyLen")
		if iterations < 1 || iterations > cryptoMaxPBKDF2Iterations {
			panic(run.newError(name + "(): iterations 必须在 1 到 " + strconv.Itoa(cryptoMaxPBKDF2Iterations) + " 之间"))
		}
		if keyLen < 1 || keyLen > cryptoMaxRandom {
			panic(run.newError(name + "(): keyLen 必须在 1 到 " + strconv.Itoa(cryptoMaxRandom) + " 之间"))
		}
		algo := "sha256"
		if text, ok := run.cryptoOptional(name, call, 4, "hash"); ok && text != "" {
			algo = strings.ToLower(text)
		}
		newHash, _ := run.cryptoHashByName(name, algo)
		derived, err := pbkdf2Derive(run.ctx, []byte(password), []byte(salt), iterations, keyLen, newHash)
		if err != nil {
			// 一次纯粹花在 Go 里的运算：只能在这里停下，按 vm 中断的同一套
			// 消息（超时/取消）结束这次执行。
			panic(run.abort())
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(derived))
	})

	_ = api.Set("jwtSign", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.jwtSign"
		payload := call.Argument(0)
		if payload == nil || goja.IsUndefined(payload) || goja.IsNull(payload) {
			panic(run.newError(name + "(): 缺少参数 payload"))
		}
		secret := run.cryptoString(name, call, 1, "secretOrKey")
		algo := "HS256"
		if text, ok := run.cryptoOptional(name, call, 2, "algo"); ok && text != "" {
			algo = strings.ToUpper(text)
		}
		payloadJSON, err := json.Marshal(payload.Export())
		if err != nil {
			panic(run.newError(name + "(): payload 必须可序列化为 JSON：" + err.Error()))
		}
		headerJSON := []byte(`{"alg":"` + algo + `","typ":"JWT"}`)
		input := cryptoB64URL(headerJSON) + "." + cryptoB64URL(payloadJSON)
		return vm.ToValue(input + "." + cryptoB64URL(run.jwtSign(name, algo, secret, input)))
	})
	_ = api.Set("jwtVerify", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.jwtVerify"
		token := run.cryptoString(name, call, 0, "token")
		secret := run.cryptoString(name, call, 1, "secretOrKey")
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			panic(run.newError(name + "(): token 必须是 header.payload.signature 三段式"))
		}
		headerJSON, err := cryptoDecodeBase64URL(parts[0])
		if err != nil {
			panic(run.newError(name + "(): header 解析失败：" + err.Error()))
		}
		var header struct {
			Alg string `json:"alg"`
		}
		if err := json.Unmarshal(headerJSON, &header); err != nil {
			panic(run.newError(name + "(): header 解析失败：" + err.Error()))
		}
		algo := strings.ToUpper(header.Alg)
		if text, ok := run.cryptoOptional(name, call, 2, "algo"); ok && text != "" {
			expected := strings.ToUpper(text)
			if algo != expected {
				panic(run.newError(name + "(): token 的 alg " + strconv.Quote(header.Alg) + " 与期望的 " + strconv.Quote(expected) + " 不一致"))
			}
			algo = expected
		}
		sig, err := cryptoDecodeBase64URL(parts[2])
		if err != nil {
			panic(run.newError(name + "(): signature 解析失败：" + err.Error()))
		}
		if !run.jwtVerify(name, algo, secret, parts[0]+"."+parts[1], sig) {
			panic(run.newError(name + "(): 签名校验失败"))
		}
		payloadJSON, err := cryptoDecodeBase64URL(parts[1])
		if err != nil {
			panic(run.newError(name + "(): payload 解析失败：" + err.Error()))
		}
		var payload any
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			panic(run.newError(name + "(): payload 不是合法 JSON：" + err.Error()))
		}
		return vm.ToValue(payload)
	})

	_ = api.Set("bytesToBase64", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.bytesToBase64"
		raw := run.cryptoBytes(name, call, 0, "bytes")
		return vm.ToValue(base64.StdEncoding.EncodeToString(raw))
	})
	_ = api.Set("bytesFromBase64", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.bytesFromBase64"
		text := run.cryptoString(name, call, 0, "text")
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			panic(run.newError(name + "(): base64 解码失败：" + err.Error()))
		}
		return run.cryptoUint8Array(name, raw)
	})
	_ = api.Set("bytesToHex", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.bytesToHex"
		raw := run.cryptoBytes(name, call, 0, "bytes")
		return vm.ToValue(hex.EncodeToString(raw))
	})
	_ = api.Set("bytesFromHex", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.bytesFromHex"
		text := run.cryptoString(name, call, 0, "text")
		raw, err := hex.DecodeString(text)
		if err != nil {
			panic(run.newError(name + "(): hex 解码失败：" + err.Error()))
		}
		return run.cryptoUint8Array(name, raw)
	})

	_ = api.Set("randomBytes", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(base64.StdEncoding.EncodeToString(run.cryptoRandom("nd.crypto.randomBytes", call)))
	})
	_ = api.Set("randomHex", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(hex.EncodeToString(run.cryptoRandom("nd.crypto.randomHex", call)))
	})

	return api
}

// cryptoString reads argument i as text. A missing value or a non-string is a
// script error, so a mistaken call reports itself where it is written instead
// of coercing silently.
func (run *Run) cryptoString(name string, call goja.FunctionCall, i int, label string) string {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 " + label))
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(run.newError(name + "(): 参数 " + label + " 必须是字符串"))
	}
	return text
}

// cryptoOptional reads argument i as text; absent, null and undefined all read
// as "no value given".
func (run *Run) cryptoOptional(name string, call goja.FunctionCall, i int, label string) (string, bool) {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", false
	}
	text, ok := value.Export().(string)
	if !ok {
		panic(run.newError(name + "(): 参数 " + label + " 必须是字符串"))
	}
	return text, true
}

// cryptoNumber reads argument i as an integer; a missing value or a non-number
// is a script error.
func (run *Run) cryptoNumber(name string, call goja.FunctionCall, i int, label string) int {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 " + label))
	}
	switch value.Export().(type) {
	case int, int32, int64, float32, float64:
	default:
		panic(run.newError(name + "(): 参数 " + label + " 必须是数字"))
	}
	return int(value.ToInteger())
}

// cryptoEncoding reads the optional encoding argument: hex unless the script
// asks for something else.
func (run *Run) cryptoEncoding(name string, call goja.FunctionCall, i int) string {
	encoding := "hex"
	if text, ok := run.cryptoOptional(name, call, i, "encoding"); ok && text != "" {
		encoding = strings.ToLower(text)
	}
	switch encoding {
	case "hex", "base64", "base64url":
		return encoding
	}
	panic(run.newError(name + "(): 不支持的 encoding " + strconv.Quote(encoding) + "，可用 hex / base64 / base64url"))
}

// cryptoEncode renders digest bytes in one of the encodings cryptoEncoding
// accepts.
func cryptoEncode(encoding string, raw []byte) string {
	switch encoding {
	case "base64":
		return base64.StdEncoding.EncodeToString(raw)
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(raw)
	default: // "hex"
		return hex.EncodeToString(raw)
	}
}

// cryptoB64URL is URL-safe base64 without padding, the alphabet JWT uses.
func cryptoB64URL(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// cryptoDecodeBase64URL takes both spellings of URL-safe base64: padding is
// dropped by the encoders here, but tokens copied from elsewhere often keep it.
func cryptoDecodeBase64URL(s string) ([]byte, error) {
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// cryptoHashByName maps an algorithm name onto its constructor and its
// crypto.Hash identity; RSA needs the identity, HMAC only the constructor.
func (run *Run) cryptoHashByName(name, algo string) (func() hash.Hash, crypto.Hash) {
	switch strings.ToLower(algo) {
	case "md5":
		return md5.New, crypto.MD5
	case "sha1":
		return sha1.New, crypto.SHA1
	case "sha224":
		return sha256.New224, crypto.SHA224
	case "sha256":
		return sha256.New, crypto.SHA256
	case "sha384":
		return sha512.New384, crypto.SHA384
	case "sha512":
		return sha512.New, crypto.SHA512
	}
	panic(run.newError(name + "(): 不支持的 algo " + strconv.Quote(algo) + "，可用 md5 / sha1 / sha224 / sha256 / sha384 / sha512"))
}

// cryptoHash maps an hmac algo name onto its constructor.
func (run *Run) cryptoHash(name, algo string) func() hash.Hash {
	newHash, _ := run.cryptoHashByName(name, algo)
	return newHash
}

// cryptoRSAHash reads the optional hash argument of an RSA call (sha256 by
// default). md5 is deliberately excluded: no RSA signature scheme should use
// it.
func (run *Run) cryptoRSAHash(name string, call goja.FunctionCall, i int) (crypto.Hash, func() hash.Hash) {
	algo := "sha256"
	if text, ok := run.cryptoOptional(name, call, i, "hash"); ok && text != "" {
		algo = strings.ToLower(text)
	}
	if algo == "md5" {
		panic(run.newError(name + "(): RSA 不允许使用 md5，可用 sha1 / sha224 / sha256 / sha384 / sha512"))
	}
	newHash, ch := run.cryptoHashByName(name, algo)
	return ch, newHash
}

// cryptoDigest hashes data for an RSA operation.
func cryptoDigest(newHash func() hash.Hash, data string) []byte {
	sum := newHash()
	_, _ = sum.Write([]byte(data))
	return sum.Sum(nil)
}

// aesArgs reads the (data, key, iv, mode) arguments the symmetric cipher pairs
// share. Key and iv accept the hex:/b64:/raw: prefixes, and plain base64 when
// unprefixed. iv may be omitted: ECB takes none and GCM generates or reads one.
func (run *Run) aesArgs(name string, call goja.FunctionCall) (data string, key, iv []byte, mode string) {
	data = run.cryptoString(name, call, 0, "data")
	key = run.cryptoBytesArg(name, "key", run.cryptoString(name, call, 1, "key"))
	if text, ok := run.cryptoOptional(name, call, 2, "iv"); ok && text != "" {
		iv = run.cryptoBytesArg(name, "iv", text)
	}

	mode = "cbc"
	if text, ok := run.cryptoOptional(name, call, 3, "mode"); ok && text != "" {
		mode = strings.ToLower(text)
	}
	switch mode {
	case "cbc", "ecb", "gcm":
		return data, key, iv, mode
	}
	panic(run.newError(name + "(): 不支持的 mode " + strconv.Quote(mode) + "，可用 cbc / ecb / gcm"))
}

// cryptoBytesArg reads a key or iv argument: "hex:", "b64:" and "raw:" select
// the encoding, and anything without a prefix stays standard base64, which
// keeps existing scripts working.
func (run *Run) cryptoBytesArg(name, label, text string) []byte {
	switch {
	case strings.HasPrefix(text, "hex:"):
		raw, err := hex.DecodeString(text[len("hex:"):])
		if err != nil {
			panic(run.newError(name + "(): 参数 " + label + " 不是合法 hex：" + err.Error()))
		}
		return raw
	case strings.HasPrefix(text, "b64:"):
		return run.cryptoBase64(name, label, text[len("b64:"):])
	case strings.HasPrefix(text, "raw:"):
		return []byte(text[len("raw:"):])
	}
	return run.cryptoBase64(name, label, text)
}

// cryptoBytes reads argument i as raw bytes: a Uint8Array (which goja exports as
// []byte) or an ArrayBuffer. A string is refused because it would travel as
// UTF-16 code units and truncate bytes above 0x7f.
func (run *Run) cryptoBytes(name string, call goja.FunctionCall, i int, label string) []byte {
	value := call.Argument(i)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 " + label))
	}
	switch raw := value.Export().(type) {
	case []byte:
		return raw
	case goja.ArrayBuffer:
		return raw.Bytes()
	}
	panic(run.newError(name + "(): 参数 " + label + " 必须是 Uint8Array 或 ArrayBuffer"))
}

// cryptoUint8Array wraps raw bytes in a real Uint8Array. vm.ToValue([]byte)
// would hand back a Go-slice-backed Array, so the typed array is built through
// its constructor instead.
func (run *Run) cryptoUint8Array(name string, raw []byte) goja.Value {
	array, err := run.vm.New(run.vm.Get("Uint8Array"), run.vm.ToValue(run.vm.NewArrayBuffer(raw)))
	if err != nil {
		panic(run.newError(name + "(): " + ScriptErrorText(err)))
	}
	return array
}

// cryptoBase64 decodes a standard-base64 argument such as a key, iv, signature
// or ciphertext.
func (run *Run) cryptoBase64(name, label, text string) []byte {
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		panic(run.newError(name + "(): 参数 " + label + " 不是合法 base64：" + err.Error()))
	}
	return raw
}

// aesBlock checks the key length and builds the cipher. AES takes 16, 24 or 32
// bytes; anything else is a script mistake worth naming.
func (run *Run) aesBlock(name string, key []byte) cipher.Block {
	switch len(key) {
	case 16, 24, 32:
	default:
		panic(run.newError(name + "(): key 必须是 16 / 24 / 32 字节（AES-128/192/256），当前 " + strconv.Itoa(len(key)) + " 字节"))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	return block
}

// desBlock picks single DES for an 8-byte key and 3DES for 16 or 24.
func (run *Run) desBlock(name string, key []byte) cipher.Block {
	var (
		block cipher.Block
		err   error
	)
	switch len(key) {
	case 8:
		block, err = des.NewCipher(key)
	case 16:
		// Two-key 3DES (K1, K2, K1): crypto/des only takes the 24-byte form.
		expanded := make([]byte, 24)
		copy(expanded, key)
		copy(expanded[16:], key[:8])
		block, err = des.NewTripleDESCipher(expanded)
	case 24:
		block, err = des.NewTripleDESCipher(key)
	default:
		panic(run.newError(name + "(): key 必须是 8 字节（DES）或 16 / 24 字节（3DES），当前 " + strconv.Itoa(len(key)) + " 字节"))
	}
	if err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	return block
}

// aesGCM builds the AEAD. The nonce is checked by aesGCMNonce only when the
// script supplies one: encryption generates a fresh nonce otherwise, and
// decryption reads it from the ciphertext.
func (run *Run) aesGCM(name string, block cipher.Block) cipher.AEAD {
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	return gcm
}

// aesGCMNonce checks a nonce the script supplied itself.
func (run *Run) aesGCMNonce(name string, nonce []byte, size int) []byte {
	if len(nonce) != size {
		panic(run.newError(name + "(): GCM 的 iv（nonce）必须是 " + strconv.Itoa(size) + " 字节，当前 " + strconv.Itoa(len(nonce)) + " 字节"))
	}
	return nonce
}

// blockEncrypt encrypts data with PKCS#7 padding in CBC or ECB mode. GCM goes
// through aesEncrypt directly, because it needs a nonce rather than an iv.
func (run *Run) blockEncrypt(name string, block cipher.Block, iv []byte, mode string, data []byte) []byte {
	blockSize := block.BlockSize()
	switch mode {
	case "ecb":
		if len(iv) != 0 {
			panic(run.newError(name + "(): mode ecb 要求 iv 为空"))
		}
		return ecbCrypt(block, pkcs7Pad(data, blockSize), true)
	case "cbc":
		if len(iv) != blockSize {
			panic(run.newError(name + "(): CBC 的 iv 必须是 " + strconv.Itoa(blockSize) + " 字节，当前 " + strconv.Itoa(len(iv)) + " 字节"))
		}
		padded := pkcs7Pad(data, blockSize)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
		return out
	}
	panic(run.newError(name + "(): 不支持的 mode " + strconv.Quote(mode) + "，可用 cbc / ecb"))
}

// blockDecrypt reverses blockEncrypt; ciphertexts that fail their padding check
// raise a script error instead of returning garbage.
func (run *Run) blockDecrypt(name string, block cipher.Block, iv []byte, mode string, raw []byte) []byte {
	blockSize := block.BlockSize()
	if len(raw) == 0 || len(raw)%blockSize != 0 {
		panic(run.newError(name + "(): 密文长度 " + strconv.Itoa(len(raw)) + " 字节不是 " + strconv.Itoa(blockSize) + " 的整数倍"))
	}
	switch mode {
	case "ecb":
		if len(iv) != 0 {
			panic(run.newError(name + "(): mode ecb 要求 iv 为空"))
		}
		return run.pkcs7Unpad(name, ecbCrypt(block, raw, false), blockSize)
	case "cbc":
		if len(iv) != blockSize {
			panic(run.newError(name + "(): CBC 的 iv 必须是 " + strconv.Itoa(blockSize) + " 字节，当前 " + strconv.Itoa(len(iv)) + " 字节"))
		}
		plain := make([]byte, len(raw))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
		return run.pkcs7Unpad(name, plain, blockSize)
	}
	panic(run.newError(name + "(): 不支持的 mode " + strconv.Quote(mode) + "，可用 cbc / ecb"))
}

// ecbCrypt applies the block cipher with a zero IV block by block, which is what
// ECB is; the standard library deliberately ships no ECB mode helper.
func ecbCrypt(block cipher.Block, data []byte, encrypt bool) []byte {
	blockSize := block.BlockSize()
	out := make([]byte, len(data))
	zeros := make([]byte, blockSize)
	for off := 0; off+blockSize <= len(data); off += blockSize {
		if encrypt {
			cipher.NewCBCEncrypter(block, zeros).CryptBlocks(out[off:off+blockSize], data[off:off+blockSize])
		} else {
			cipher.NewCBCDecrypter(block, zeros).CryptBlocks(out[off:off+blockSize], data[off:off+blockSize])
		}
	}
	return out
}

// pkcs7Pad appends PKCS#7 padding to a multiple of blockSize, so the last block
// of a ciphertext says how much to strip on the way back.
func pkcs7Pad(data []byte, blockSize int) []byte {
	fill := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+fill)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(fill)
	}
	return out
}

// pkcs7Unpad strips that padding and refuses bytes no correct key and iv could
// have produced, so a wrong key reads as a decode error instead of garbage.
func (run *Run) pkcs7Unpad(name string, data []byte, blockSize int) []byte {
	if len(data) == 0 || len(data)%blockSize != 0 {
		panic(run.newError(name + "(): 密文长度 " + strconv.Itoa(len(data)) + " 字节不是 " + strconv.Itoa(blockSize) + " 的整数倍"))
	}
	fill := int(data[len(data)-1])
	if fill == 0 || fill > blockSize || fill > len(data) {
		panic(run.newError(name + "(): 解密失败：PKCS#7 填充不合法（key 或 iv 可能不对）"))
	}
	for _, b := range data[len(data)-fill:] {
		if int(b) != fill {
			panic(run.newError(name + "(): 解密失败：PKCS#7 填充不合法（key 或 iv 可能不对）"))
		}
	}
	return data[:len(data)-fill]
}

// rsaPrivateKey parses a PEM private key in PKCS#1 ("RSA PRIVATE KEY") or PKCS#8
// ("PRIVATE KEY") form.
func (run *Run) rsaPrivateKey(name, text string) *rsa.PrivateKey {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		panic(run.newError(name + "(): 私钥不是 PEM 格式"))
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		panic(run.newError(name + "(): 无法解析私钥（需要 PKCS#1 或 PKCS#8 PEM）：" + err.Error()))
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		panic(run.newError(name + "(): PKCS#8 私钥不是 RSA"))
	}
	return key
}

// rsaPublicKey parses a PEM public key in PKIX ("PUBLIC KEY") or PKCS#1
// ("RSA PUBLIC KEY") form.
func (run *Run) rsaPublicKey(name, text string) *rsa.PublicKey {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		panic(run.newError(name + "(): 公钥不是 PEM 格式"))
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			panic(run.newError(name + "(): PKIX 公钥不是 RSA"))
		}
		return key
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		panic(run.newError(name + "(): 无法解析公钥（需要 PKIX 或 PKCS#1 PEM）：" + err.Error()))
	}
	return key
}

// pbkdf2Batch is how many hash rounds run between two context checks: often
// enough to stop promptly once the run ends, rare enough that the check costs
// nothing next to the hashing around it.
const pbkdf2Batch = 256

// pbkdf2Derive derives a key per RFC 2898 using only the standard library; it is
// the same construction golang.org/x/crypto/pbkdf2 implements. It is the one call
// in this package that can burn seconds inside Go, where a vm interrupt cannot
// reach it, so it watches ctx and returns its error instead of finishing.
func pbkdf2Derive(ctx context.Context, password, salt []byte, iterations, keyLen int, newHash func() hash.Hash) ([]byte, error) {
	mac := hmac.New(newHash, password)
	hashLen := mac.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	derived := make([]byte, 0, blocks*hashLen)
	u := make([]byte, hashLen)
	aborted := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	for block := 1; block <= blocks; block++ {
		mac.Reset()
		_, _ = mac.Write(salt)
		_, _ = mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u = mac.Sum(u[:0])
		t := bytes.Clone(u)
		for n := 2; n <= iterations; n++ {
			if n%pbkdf2Batch == 0 {
				if err := aborted(); err != nil {
					return nil, err
				}
			}
			mac.Reset()
			_, _ = mac.Write(u)
			u = mac.Sum(u[:0])
			for x := range t {
				t[x] ^= u[x]
			}
		}
		derived = append(derived, t...)
	}
	// The last batch may be shorter than pbkdf2Batch, so check once at the end too.
	if err := aborted(); err != nil {
		return nil, err
	}
	return derived[:keyLen], nil
}

// jwtSign signs the signing input for one of the supported JWT algorithms. HS*
// takes a string secret; RS* takes a PKCS#1/PKCS#8 PEM private key.
func (run *Run) jwtSign(name, algo, secret, input string) []byte {
	switch algo {
	case "HS256", "HS384", "HS512":
		var newHash func() hash.Hash
		switch algo {
		case "HS256":
			newHash = sha256.New
		case "HS384":
			newHash = sha512.New384
		default:
			newHash = sha512.New
		}
		mac := hmac.New(newHash, []byte(secret))
		_, _ = mac.Write([]byte(input))
		return mac.Sum(nil)
	case "RS256", "RS384", "RS512":
		ch, newHash := jwtHash(algo)
		key := run.rsaPrivateKey(name, secret)
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, ch, cryptoDigest(newHash, input))
		if err != nil {
			panic(run.newError(name + "(): " + err.Error()))
		}
		return sig
	}
	panic(run.newError(name + "(): 不支持的 algo " + strconv.Quote(algo) + "，可用 HS256 / HS384 / HS512 / RS256 / RS384 / RS512"))
}

// jwtVerify reports whether sig is a valid jwtSign signature of input.
func (run *Run) jwtVerify(name, algo, secret, input string, sig []byte) bool {
	switch algo {
	case "HS256", "HS384", "HS512":
		return hmac.Equal(sig, run.jwtSign(name, algo, secret, input))
	case "RS256", "RS384", "RS512":
		ch, newHash := jwtHash(algo)
		key := run.rsaPublicKey(name, secret)
		return rsa.VerifyPKCS1v15(key, ch, cryptoDigest(newHash, input), sig) == nil
	}
	panic(run.newError(name + "(): 不支持的 algo " + strconv.Quote(algo) + "，可用 HS256 / HS384 / HS512 / RS256 / RS384 / RS512"))
}

// jwtHash maps one RS* algorithm onto the hash it signs with.
func jwtHash(algo string) (crypto.Hash, func() hash.Hash) {
	switch algo {
	case "RS256":
		return crypto.SHA256, sha256.New
	case "RS384":
		return crypto.SHA384, sha512.New384
	default: // "RS512"
		return crypto.SHA512, sha512.New
	}
}

// cryptoEncodeURIComponent percent-encodes s per RFC 3986: the unreserved
// characters pass through, everything else becomes %XX with uppercase hex.
// Unlike url.QueryEscape it never emits "+" and encodes "/", which is what API
// signature algorithms expect.
func cryptoEncodeURIComponent(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
		}
	}
	return b.String()
}

// cryptoRandom reads the byte count of the random helpers and fills that many
// bytes from crypto/rand.
func (run *Run) cryptoRandom(name string, call goja.FunctionCall) []byte {
	n := run.cryptoNumber(name, call, 0, "n")
	if n < 1 || n > cryptoMaxRandom {
		panic(run.newError(name + "(): n 必须在 1 到 " + strconv.Itoa(cryptoMaxRandom) + " 之间"))
	}

	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	return raw
}
