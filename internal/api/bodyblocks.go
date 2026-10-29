package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The body editor's wire form (WL-REQ-1867): a GET with ?as=blocks adds the
// body as blocks, and a body write may carry blocks in place of Markdown.
//
// No dedicated metric: a refused ?as= or blocks write is
// http_requests_total's {route, code}, and the conversion a blocks write
// feeds is counted by the store's normalisation metric.

// asBlocks reports whether p asks for blocks. Any value of as but "blocks"
// is refused: it writes 422 and returns ok=false.
func asBlocks(w http.ResponseWriter, p model.BodyParams) (blocks, ok bool) {
	switch p.As {
	case "":
		return false, true
	case "blocks":
		return true, true
	}
	writeErr(w, http.StatusUnprocessableEntity, `as must be "blocks"`)
	return false, false
}

// bodyBlocks is body served as blocks; subject is "doc", "rule" or "task".
func bodyBlocks(subject string, body model.Markdown) (model.BlocksBody, error) {
	blocks, err := mdblocks.Parse([]byte(body), mdblocks.Subject(subject))
	if err != nil {
		return model.BlocksBody{}, err
	}
	sum := sha256.Sum256([]byte(body))
	return model.BlocksBody{Blocks: blocks, BodySHA256: hex.EncodeToString(sum[:])}, nil
}

// blocksMarkdown converts a blocks write to the Markdown the write path
// takes. hasBody says the request also carried body, which is refused. On a
// refusal it writes 422 and returns ok=false.
func blocksMarkdown(w http.ResponseWriter, hasBody bool, blocks []model.EditorBlock) (model.Markdown, bool) {
	if hasBody {
		writeErr(w, http.StatusUnprocessableEntity, "send body or blocks, not both (WL-REQ-1867)")
		return "", false
	}
	for _, b := range blocks {
		if err := b.Validate(); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return "", false
		}
	}
	md, err := mdblocks.Write(blocks)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return "", false
	}
	return model.Markdown(md), true
}
