package weread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ShelfBook 是书架条目里我们关心的字段;响应里还有进度、分组等字段,按需再扩。
type ShelfBook struct {
	BookID string `json:"bookId"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Cover  string `json:"cover"`
	Intro  string `json:"intro"`
	Format string `json:"format"`
}

type ShelfSync struct {
	Books []ShelfBook `json:"books"`
}

// ShelfSync 拉取书架。移动端接口,凭据走 vid/accessToken 请求头,无需签名。
func (c *Client) ShelfSync(ctx context.Context, creds *Credentials) (*ShelfSync, error) {
	if creds == nil || creds.AccessToken == "" {
		return nil, fmt.Errorf("凭据不完整,无法获取书架")
	}
	headers := versionHeaders()
	for k, v := range authHeaders(creds) {
		headers[k] = v
	}
	body, status, err := c.getJSON(ctx, BaseURL+"/shelf/sync", headers)
	if err != nil {
		return nil, fmt.Errorf("获取书架失败: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("获取书架失败: HTTP %d", status)
	}
	if err := checkBusinessCode(body); err != nil {
		return nil, err
	}
	var shelf ShelfSync
	if err := json.Unmarshal(body, &shelf); err != nil {
		return nil, fmt.Errorf("书架响应解析失败: %w", err)
	}
	// 响应里可能混有听书专辑等非书籍条目(无 bookId),过滤掉。
	books := make([]ShelfBook, 0, len(shelf.Books))
	for _, b := range shelf.Books {
		if b.BookID != "" {
			books = append(books, b)
		}
	}
	shelf.Books = books
	return &shelf, nil
}
