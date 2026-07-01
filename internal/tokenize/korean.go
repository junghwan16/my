// Package tokenize provides the default, pure-Go Korean morphological tokenizer
// used to index and query memory text. It wraps Kagome with the Korean
// dictionary (mecab-ko-dic), so it adds no cgo and keeps the single-binary build.
package tokenize

import (
	"strings"

	ko "github.com/ikawaha/kagome-dict-ko"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

// Korean tokenizes text into lowercased morpheme surfaces. It satisfies
// memory.Tokenizer.
type Korean struct {
	t *tokenizer.Tokenizer
}

// NewKorean builds a Korean tokenizer backed by the embedded mecab-ko-dic.
func NewKorean() (*Korean, error) {
	t, err := tokenizer.New(ko.Dict(), tokenizer.OmitBosEos())
	if err != nil {
		return nil, err
	}
	return &Korean{t: t}, nil
}

// Tokenize returns the lowercased morpheme surfaces of text, dropping empties.
// Splitting "종목을" into "종목"+"을" is what lets an FTS query for the bare noun
// "종목" match text where it appears with an attached josa.
func (k *Korean) Tokenize(text string) []string {
	morphs := k.t.Tokenize(text)
	out := make([]string, 0, len(morphs))
	for _, m := range morphs {
		s := strings.ToLower(strings.TrimSpace(m.Surface))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
