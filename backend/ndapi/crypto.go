package ndapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// cryptoMaxRandom caps the byte count nd.crypto.randomBytes/randomHex accept, so
// a typo cannot ask for an unbounded allocation.
const cryptoMaxRandom = 4096

// CryptoGlobal is nd.crypto: digests, HMAC, AES and the text codecs. Every
// argument and every result is a string: nd.vars only carries text, so byte
// slices never cross into the script.
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
	digest("sha256", sha256.New)
	digest("sha512", sha512.New)

	_ = api.Set("hmac", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.hmac"
		algo := run.cryptoString(name, call, 0, "algo")
		data := run.cryptoString(name, call, 1, "data")
		key := run.cryptoString(name, call, 2, "key")
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

	_ = api.Set("aesEncrypt", func(call goja.FunctionCall) goja.Value {
		const name = "nd.crypto.aesEncrypt"
		data, key, iv, mode := run.aesArgs(name, call)
		block := run.aesBlock(name, key)

		if mode == "gcm" {
			gcm := run.aesGCM(name, block, iv)
			return vm.ToValue(base64.StdEncoding.EncodeToString(gcm.Seal(nil, iv, []byte(data), nil)))
		}

		padded := pkcs7Pad([]byte(data))
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, run.cbcIV(name, iv)).CryptBlocks(out, padded)
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
			gcm := run.aesGCM(name, block, iv)
			plain, err := gcm.Open(nil, iv, raw, nil)
			if err != nil {
				panic(run.newError(name + "(): 解密失败，key / iv 或密文不匹配"))
			}
			return vm.ToValue(string(plain))
		}

		if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
			panic(run.newError(name + "(): 密文长度 " + strconv.Itoa(len(raw)) + " 字节不是 " + strconv.Itoa(aes.BlockSize) + " 的整数倍"))
		}
		plain := make([]byte, len(raw))
		cipher.NewCBCDecrypter(block, run.cbcIV(name, iv)).CryptBlocks(plain, raw)
		plain, err = pkcs7Unpad(plain)
		if err != nil {
			panic(run.newError(name + "(): 解密失败：" + err.Error()))
		}
		return vm.ToValue(string(plain))
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

// cryptoDecodeBase64URL takes both spellings of URL-safe base64: padding is
// dropped by the encoders here, but tokens copied from elsewhere often keep it.
func cryptoDecodeBase64URL(s string) ([]byte, error) {
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// cryptoHash maps an hmac algo name onto its constructor.
func (run *Run) cryptoHash(name, algo string) func() hash.Hash {
	switch strings.ToLower(algo) {
	case "md5":
		return md5.New
	case "sha1":
		return sha1.New
	case "sha256":
		return sha256.New
	case "sha512":
		return sha512.New
	}
	panic(run.newError(name + "(): 不支持的 algo " + strconv.Quote(algo) + "，可用 md5 / sha1 / sha256 / sha512"))
}

// aesArgs reads the (data, key, iv, mode) arguments the AES pair shares. Key and
// iv arrive as base64 because a script literal expresses arbitrary bytes badly.
func (run *Run) aesArgs(name string, call goja.FunctionCall) (data string, key, iv []byte, mode string) {
	data = run.cryptoString(name, call, 0, "data")
	key = run.cryptoBase64(name, "key", run.cryptoString(name, call, 1, "key"))
	iv = run.cryptoBase64(name, "iv", run.cryptoString(name, call, 2, "iv"))

	mode = "cbc"
	if text, ok := run.cryptoOptional(name, call, 3, "mode"); ok && text != "" {
		mode = strings.ToLower(text)
	}
	switch mode {
	case "cbc", "gcm":
		return data, key, iv, mode
	}
	panic(run.newError(name + "(): 不支持的 mode " + strconv.Quote(mode) + "，可用 cbc / gcm"))
}

// cryptoBase64 decodes a base64 argument of an AES call.
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

// cbcIV checks the iv of a CBC call, which must fill one block.
func (run *Run) cbcIV(name string, iv []byte) []byte {
	if len(iv) != aes.BlockSize {
		panic(run.newError(name + "(): CBC 的 iv 必须是 " + strconv.Itoa(aes.BlockSize) + " 字节，当前 " + strconv.Itoa(len(iv)) + " 字节"))
	}
	return iv
}

// aesGCM builds the AEAD and checks the nonce length up front, so the script
// gets a readable message instead of a panic from the cipher package.
func (run *Run) aesGCM(name string, block cipher.Block, iv []byte) cipher.AEAD {
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	if len(iv) != gcm.NonceSize() {
		panic(run.newError(name + "(): GCM 的 iv（nonce）必须是 " + strconv.Itoa(gcm.NonceSize()) + " 字节，当前 " + strconv.Itoa(len(iv)) + " 字节"))
	}
	return gcm
}

// cryptoRandom reads the byte count of the random helpers and fills that many
// bytes from crypto/rand.
func (run *Run) cryptoRandom(name string, call goja.FunctionCall) []byte {
	value := call.Argument(0)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		panic(run.newError(name + "(): 缺少参数 n"))
	}
	switch value.Export().(type) {
	case int, int32, int64, float32, float64:
	default:
		panic(run.newError(name + "(): 参数 n 必须是数字"))
	}

	n := value.ToInteger()
	if n < 1 || n > cryptoMaxRandom {
		panic(run.newError(name + "(): n 必须在 1 到 " + strconv.Itoa(cryptoMaxRandom) + " 之间"))
	}

	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(run.newError(name + "(): " + err.Error()))
	}
	return raw
}

// pkcs7Pad appends PKCS#7 padding, so the last block of a CBC ciphertext says
// how much to strip on the way back.
func pkcs7Pad(data []byte) []byte {
	fill := aes.BlockSize - len(data)%aes.BlockSize
	out := make([]byte, len(data)+fill)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(fill)
	}
	return out
}

// pkcs7Unpad strips that padding and refuses bytes no correct key and iv could
// have produced, so a wrong key reads as a decode error instead of garbage.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度 %d 字节不是 %d 的整数倍", len(data), aes.BlockSize)
	}
	fill := int(data[len(data)-1])
	if fill == 0 || fill > aes.BlockSize || fill > len(data) {
		return nil, fmt.Errorf("PKCS#7 填充不合法（key 或 iv 可能不对）")
	}
	for _, b := range data[len(data)-fill:] {
		if int(b) != fill {
			return nil, fmt.Errorf("PKCS#7 填充不合法（key 或 iv 可能不对）")
		}
	}
	return data[:len(data)-fill], nil
}
