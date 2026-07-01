package memories

// Tokenizer splits text into search tokens. Korean memory text needs
// morphological tokenization (josa/eomi stripped) for FTS5 to match short query
// terms; the concrete analyzer lives behind this interface so it can be swapped
// (e.g. a more accurate cgo analyzer, or a future semantic ranker) without
// changing the recall contract. The same Tokenizer must be used to index and to
// query, so a term indexed one way is not missed because the query split
// differently.
type Tokenizer interface {
	Tokenize(text string) []string
}
