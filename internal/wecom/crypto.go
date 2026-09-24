package wecom

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// 协议出处（企业微信开发者中心）：
//   - 回调配置/加解密：developer.work.weixin.qq.com/document/path/90930
//     URL+Token+EncodingAESKey；GET 验证回 echostr 明文；POST 收 XML（ToUserName/AgentID/Encrypt）。
//   - 主动发应用消息：document/path/90235
//     POST https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=... {touser, agentid, msgtype, text}
//   - access_token：document/path/91039
//     GET /cgi-bin/gettoken?corpid=..&corpsecret=..，有效期 7200s，提前刷新。
//
// 本文件只做 WXBizMsgCrypt 等价实现（SHA1 签名 + AES-256-CBC 加解密）：
//   - signature = sha1(sort(token, timestamp, nonce, encrypt))
//   - 明文包 = random(16) + msg_len(4字节网络序) + msg + corpId
//   - PKCS7(32) 补位后 AES-256-CBC 加密，key = base64decode(EncodingAESKey+"=")，iv = key 前 16 字节
//   - 密文 base64 即 Encrypt；解密逆向，并校验 corpId。

// Sign 计算回调签名：sha1(sort(token, timestamp, nonce, encrypt))。
func Sign(token, timestamp, nonce, encrypt string) string {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return fmt.Sprintf("%x", h)
}

// VerifySignature 比对签名（恒定时间防时序探测倒不必，企微侧 1 秒验证超时，
// 直接比即可；这里用 subtle 常量时间，顺手）。
func VerifySignature(token, timestamp, nonce, encrypt, signature string) bool {
	want := Sign(token, timestamp, nonce, encrypt)
	if len(want) != len(signature) {
		return false
	}
	diff := 0
	for i := range want {
		diff |= int(want[i]) ^ int(signature[i])
	}
	return diff == 0
}

func aesKey(encodingAES string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodingAES + "=")
	if err != nil {
		return nil, fmt.Errorf("bad EncodingAESKey: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("bad EncodingAESKey length %d", len(key))
	}
	return key, nil
}

// Encrypt 把明文 XML 打包加密，返回 base64 密文。
func Encrypt(encodingAES, plainXML, corpID string) (string, error) {
	key, err := aesKey(encodingAES)
	if err != nil {
		return "", err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	msg := []byte(plainXML)
	buf := make([]byte, 0, 16+4+len(msg)+len(corpID))
	buf = append(buf, random...)
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, uint32(len(msg)))
	buf = append(buf, lenBytes...)
	buf = append(buf, msg...)
	buf = append(buf, []byte(corpID)...)
	buf = pkcs7Pad(buf, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := key[:16]
	mode := cipher.NewCBCEncrypter(block, iv)
	out := make([]byte, len(buf))
	mode.CryptBlocks(out, buf)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt 解 base64 密文，校验 corpID，返回明文 XML。
func Decrypt(encodingAES, encryptB64, corpID string) (string, error) {
	msg, receiveID, err := DecryptAny(encodingAES, encryptB64)
	if err != nil {
		return "", err
	}
	if corpID != "" && receiveID != corpID {
		return "", fmt.Errorf("corpId mismatch")
	}
	return msg, nil
}

// DecryptAny 解 base64 密文，不校验 receiveID（corpId 未知时用），
// 返回 (明文, receiveid)。receiveid 对自建应用就是 corpId——
// 首次配置只拿到 Secret/Token/AESKey 没填 corpId 时，靠它自动学会。
func DecryptAny(encodingAES, encryptB64 string) (string, string, error) {
	key, err := aesKey(encodingAES)
	if err != nil {
		return "", "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encryptB64)
	if err != nil {
		return "", "", fmt.Errorf("bad encrypt: %w", err)
	}
	if len(raw) == 0 || len(raw)%32 != 0 {
		return "", "", fmt.Errorf("bad encrypt length %d", len(raw))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	iv := key[:16]
	mode := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(raw))
	mode.CryptBlocks(plain, raw)
	plain, err = pkcs7Unpad(plain, 32)
	if err != nil {
		return "", "", err
	}
	if len(plain) < 20 {
		return "", "", fmt.Errorf("plain too short")
	}
	msgLen := binary.BigEndian.Uint32(plain[16:20])
	if int(20+msgLen) > len(plain) {
		return "", "", fmt.Errorf("bad msg_len %d", msgLen)
	}
	msg := plain[20 : 20+msgLen]
	receiveID := string(plain[20+msgLen:])
	return string(msg), receiveID, nil
}

func pkcs7Pad(b []byte, blockSize int) []byte {
	pad := blockSize - len(b)%blockSize
	if pad == 0 {
		pad = blockSize
	}
	return append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 || len(b)%blockSize != 0 {
		return nil, fmt.Errorf("bad padding")
	}
	pad := int(b[len(b)-1])
	if pad < 1 || pad > blockSize {
		return nil, fmt.Errorf("bad padding byte %d", pad)
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, fmt.Errorf("bad padding content")
		}
	}
	return b[:len(b)-pad], nil
}
