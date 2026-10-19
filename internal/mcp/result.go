package mcp

import (
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pageSize is the most items a list tool returns per call.
const pageSize = 10

// errInvalidCursor is a cursor that is unsigned, tampered or malformed. The
// dispatcher reports it as outcome invalid.
var errInvalidCursor = errors.New("The page cursor is not valid. Start the list again without a cursor.")

// say builds a tool result: the first content block is one sentence for the
// voice, the second the detail. A string detail is used as-is (callers render
// with the matching cli.*Render), nil adds no block, anything else is JSON.
func say(sentence string, detail any) *mcp.CallToolResult {
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sentence}}}
	var text string
	switch d := detail.(type) {
	case nil:
		return res
	case string:
		text = d
	default:
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return errorResult("The result could not be rendered.")
		}
		text = string(b)
	}
	res.Content = append(res.Content, &mcp.TextContent{Text: text})
	return res
}

func cursorTag(secret string, offset int) string {
	return hex.EncodeToString(mac8([]byte(secret), []byte("cursor\x00"+strconv.Itoa(offset))))
}

// pageOf returns the slice of items the cursor points at and, when more
// remain, a signed cursor for the next slice (`<offset>.<tag>`, WL-REQ-1828).
// An empty cursor starts at the beginning.
func pageOf[T any](items []T, cursor, secret string) (out []T, next string, err error) {
	offset := 0
	if cursor != "" {
		off, tag, ok := strings.Cut(cursor, ".")
		n, convErr := strconv.Atoi(off)
		if !ok || convErr != nil || n < 0 || !hmac.Equal([]byte(tag), []byte(cursorTag(secret, n))) {
			return nil, "", errInvalidCursor
		}
		offset = n
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + pageSize
	if end >= len(items) {
		return items[offset:], "", nil
	}
	return items[offset:end], strconv.Itoa(end) + "." + cursorTag(secret, end), nil
}
