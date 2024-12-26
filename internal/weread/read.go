// 网页版阅读上报协议:POST /web/book/read。
// 签名算法(sg/s)与 appId 生成规则逆向自微信读书网页前端,已与社区参考实现逐字对齐;
// 常量与算法须与前端保持一致,不可改动。
package weread

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// readSignatureKey 是网页前端内嵌的固定盐,sg = SHA256(ts + rn + KEY)。
const readSignatureKey = "3c5c8717f3daf09iop3423zafeqoi"

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// getWebAppID 从 User-Agent 确定性推导网页端 appId(wb...h...),
// 同一个 UA 永远得到同一个 appId,与前端行为一致。
func getWebAppID(ua string) string {
	parts := strings.Split(ua, " ")
	n := len(parts)
	if n > 12 {
		n = 12
	}
	rnd1 := ""
	for _, p := range parts[:n] {
		rnd1 += strconv.Itoa(len(p) % 10)
	}
	num := int64(0)
	for _, ch := range ua {
		num = (131*num + int64(ch)) & 0x7FFFFFFF
	}
	rnd2 := strconv.FormatInt(num, 10)
	if len(rnd2) > 16 {
		rnd2 = rnd2[:16]
	}
	return "wb" + rnd1 + "h" + rnd2
}

// calcHash 是前端对 bookId/chapterUid 等标识的混淆编码:
// MD5 头尾 + 按十进制分段转十六进制(数字)或逐字符转十六进制(非数字)。
func calcHash(value string) string {
	dataMD5 := md5Hex(value)
	result := dataMD5[:3]
	isDigits := value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1
	kind := "4"
	var chunks []string
	if isDigits {
		kind = "3"
		for i := 0; i < len(value); i += 9 {
			end := i + 9
			if end > len(value) {
				end = len(value)
			}
			// ParseUint 会丢掉前导零,与 Python int() 行为一致。
			n, err := strconv.ParseUint(value[i:end], 10, 64)
			if err != nil {
				n = 0
			}
			chunks = append(chunks, strconv.FormatUint(n, 16))
		}
	} else {
		var sb strings.Builder
		for _, r := range value {
			sb.WriteString(strconv.FormatInt(int64(r), 16))
		}
		chunks = append(chunks, sb.String())
	}
	result += kind + "2" + dataMD5[len(dataMD5)-2:]
	for i, chunk := range chunks {
		length := strconv.FormatInt(int64(len(chunk)), 16)
		if len(length) < 2 {
			length = "0" + length
		}
		result += length + chunk
		if i < len(chunks)-1 {
			result += "g"
		}
	}
	if len(result) < 20 {
		result += dataMD5[:20-len(result)]
	}
	return result + md5Hex(result)[:3]
}

// signPayload 对按 key 排序、URL 编码后的全部参数做 SDBM 变体哈希,即参数签名 s。
func signPayload(payload map[string]any) string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		// 与 Python quote(s, safe='') 对齐:空格编码为 %20 而非 +。
		escaped := strings.ReplaceAll(url.QueryEscape(fmt.Sprint(payload[k])), "+", "%20")
		parts = append(parts, k+"="+escaped)
	}
	raw := strings.Join(parts, "&")
	runes := []rune(raw)
	n1, n2 := int64(0x15051505), int64(0x15051505)
	for i := len(runes) - 1; i > 0; i -= 2 {
		n1 = (n1 ^ (int64(runes[i]) << ((len(runes) - i) % 30))) & 0x7FFFFFFF
		n2 = (n2 ^ (int64(runes[i-1]) << (i % 30))) & 0x7FFFFFFF
	}
	return strconv.FormatInt(n1+n2, 16)
}

// readHeartbeat 发送一次阅读心跳(rt 秒),返回服务端是否计入(synckey 存在)。
func (c *Client) readHeartbeat(ctx context.Context, cookie, bookID string, chapterUID, offset, percent, rt int) (bool, error) {
	now := time.Now()
	payload := map[string]any{
		"appId": getWebAppID(webUserAgent),
		"b":     calcHash(bookID),
		"c":     calcHash(strconv.Itoa(chapterUID)),
		"ci":    chapterUID,
		"co":    offset,
		"ct":    now.Unix(),
		"dy":    0,
		"fm":    "epub",
		"pc":    calcHash("0"),
		"pr":    percent,
		"ps":    calcHash("0"),
		"sm":    "",
		"rt":    rt,
		"ts":    now.UnixMilli(),
		"rn":    randIntn(1000),
	}
	payload["sg"] = sha256Hex(fmt.Sprintf("%d%d%s", payload["ts"], payload["rn"], readSignatureKey))
	payload["s"] = signPayload(payload)

	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	data, status, err := c.do(ctx, http.MethodPost, webBaseURL+"/web/book/read", map[string]string{
		"User-Agent":   webUserAgent,
		"cookie":       cookie,
		"content-type": "application/json; charset=UTF-8",
	}, body)
	if err != nil {
		return false, fmt.Errorf("阅读上报失败: %w", err)
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("阅读上报失败: HTTP %d", status)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, fmt.Errorf("阅读上报响应不是 JSON: %w", err)
	}
	// 与参考实现一致:带 synckey 才算这次时长被记账。
	_, hasSucc := resp["succ"]
	_, hasSyncKey := resp["synckey"]
	if !hasSucc && !hasSyncKey {
		// 既没有 succ 也没有 synckey:大概率会话失效,交给上层续期重试。
		var envelope struct {
			ErrCode json.Number `json:"errCode"`
			ErrMsg  string      `json:"errMsg"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.ErrCode.String() == "-2012" {
			return false, ErrSessionExpired
		}
	}
	return hasSyncKey, nil
}

// ChapterUIDs 拉取一本书的章节 UID 列表(网页版接口,供阅读会话推进章节)。
func (c *Client) ChapterUIDs(ctx context.Context, cookie, bookID string) ([]int, error) {
	body, err := json.Marshal(map[string]any{"bookIds": []string{bookID}})
	if err != nil {
		return nil, err
	}
	data, status, err := c.do(ctx, http.MethodPost, webBaseURL+"/web/book/chapterInfos", map[string]string{
		"User-Agent":   webUserAgent,
		"cookie":       cookie,
		"content-type": "application/json; charset=UTF-8",
	}, body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("章节信息失败: HTTP %d", status)
	}
	var resp struct {
		Data []struct {
			BookID  string `json:"bookId"`
			Updated []struct {
				ChapterUID int `json:"chapterUid"`
			} `json:"updated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	for _, item := range resp.Data {
		if item.BookID == bookID && len(item.Updated) > 0 {
			uids := make([]int, 0, len(item.Updated))
			for _, ch := range item.Updated {
				if ch.ChapterUID > 0 {
					uids = append(uids, ch.ChapterUID)
				}
			}
			if len(uids) > 0 {
				return uids, nil
			}
		}
	}
	// 章节列表拿不到时退回第 1 章:阅读上报本身不强制依赖章节真实存在。
	return []int{1}, nil
}
