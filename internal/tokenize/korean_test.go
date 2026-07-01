package tokenize

import (
	"slices"
	"testing"

	ko "github.com/ikawaha/kagome-dict-ko"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

// TestKoreanUserDictKeepsTermsWhole proves the embedded user dictionary keeps
// domain/loan terms as single tokens instead of the over-segmentation
// mecab-ko-dic produces for unknown terms (issue #8).
func TestKoreanUserDictKeepsTermsWhole(t *testing.T) {
	k, err := NewKorean()
	if err != nil {
		t.Fatalf("NewKorean: %v", err)
	}

	cases := []struct {
		name string
		text string
		want string
	}{
		{"migration+schema", "마이그레이션 스키마", "마이그레이션"},
		{"schema", "스키마 변경", "스키마"},
		{"refactoring with josa", "리팩터링을 했다", "리팩터링"},
		{"tokenizer", "토크나이저 개선", "토크나이저"},
		{"embedding", "임베딩 벡터", "임베딩"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := k.Tokenize(tc.text)
			if !slices.Contains(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %v, want to contain whole token %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestKoreanUserDictImprovesPrecision fixes the before/after behavior for
// 마이그레이션: without the user dictionary mecab-ko-dic shatters it into
// fragments (including super-frequent noise like "이"); with it the term is one
// token. This is the precision improvement acceptance criterion of issue #8.
func TestKoreanUserDictImprovesPrecision(t *testing.T) {
	baseline, err := tokenizer.New(ko.Dict(), tokenizer.OmitBosEos())
	if err != nil {
		t.Fatalf("baseline tokenizer: %v", err)
	}
	morphs := baseline.Tokenize("마이그레이션")
	before := make([]string, 0, len(morphs))
	for _, m := range morphs {
		before = append(before, m.Surface)
	}
	if len(before) < 2 {
		t.Fatalf("expected baseline to over-segment 마이그레이션, got %v", before)
	}
	if !slices.Contains(before, "이") {
		t.Fatalf("expected baseline fragments to include noise token %q, got %v", "이", before)
	}

	k, err := NewKorean()
	if err != nil {
		t.Fatalf("NewKorean: %v", err)
	}
	after := k.Tokenize("마이그레이션")
	if !slices.Equal(after, []string{"마이그레이션"}) {
		t.Fatalf("with user dict, Tokenize(%q) = %v, want single token", "마이그레이션", after)
	}
}
