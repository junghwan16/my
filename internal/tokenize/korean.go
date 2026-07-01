// Package tokenize provides the default, pure-Go Korean morphological tokenizer
// used to index and query memory text. It wraps Kagome with the Korean
// dictionary (mecab-ko-dic), so it adds no cgo and keeps the single-binary build.
package tokenize

import (
	_ "embed"
	"fmt"
	"strings"

	ko "github.com/ikawaha/kagome-dict-ko"
	"github.com/ikawaha/kagome-dict/dict"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

// userDictCSV is the vendored, embedded user dictionary of domain/loan terms.
// Embedding it keeps the single binary self-contained (no external file) and
// pins the term set for reproducibility, matching ADR-0004's guidance to fix
// the dictionary version behind the unchanged Tokenizer contract.
//
//go:embed userdict.csv
var userDictCSV string

// loadUserDict parses the embedded user dictionary into a Kagome UserDict. It
// keeps domain/loan terms (e.g. 마이그레이션) as single morpheme tokens instead
// of the over-segmentation mecab-ko-dic produces for unknown terms.
func loadUserDict() (*dict.UserDict, error) {
	records, err := dict.NewUserDicRecords(strings.NewReader(userDictCSV))
	if err != nil {
		return nil, fmt.Errorf("parse embedded user dictionary: %w", err)
	}
	ud, err := records.NewUserDict()
	if err != nil {
		return nil, fmt.Errorf("build user dictionary: %w", err)
	}
	return ud, nil
}

// Korean tokenizes text into lowercased morpheme forms. It satisfies
// memories.Tokenizer.
type Korean struct {
	t *tokenizer.Tokenizer
}

// NewKorean builds a Korean tokenizer backed by the embedded mecab-ko-dic and
// the embedded user dictionary of domain/loan terms. It loads the user
// dictionary internally, so the memories.Tokenizer contract is unchanged and
// callers need no extra wiring.
func NewKorean() (*Korean, error) {
	ud, err := loadUserDict()
	if err != nil {
		return nil, err
	}
	t, err := tokenizer.New(ko.Dict(), tokenizer.OmitBosEos(), tokenizer.UserDict(ud))
	if err != nil {
		return nil, fmt.Errorf("build korean tokenizer: %w", err)
	}
	return &Korean{t: t}, nil
}

// Tokenize returns the lowercased morpheme forms of text, dropping empties.
// Splitting "종목을" into "종목"+"을" is what lets an FTS query for the noun
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
