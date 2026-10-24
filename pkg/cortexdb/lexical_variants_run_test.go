package cortexdb

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Six French rows hold every word of the question, so the question alone
// fills top_k. fr5 says the same at more length, so the question's own query
// ranks it last, and it is the only row that also speaks of the consultation.
// or-only holds two of the question's words in a short row; de-answer is a
// German row only a German variant finds.
const fillQuestion = "conseil procédure cantons"

func openFilledStore(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "filled.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.HasEmbedder() {
		t.Fatal("a lexical test must run without an embedder")
	}
	ctx := context.Background()
	save := func(id, content string) {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "kb"}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	for i := 0; i < 6; i++ {
		text := fmt.Sprintf("Le conseil fédéral règle la procédure numéro %d des cantons, après avoir entendu les communes, "+
			"les partis politiques et les milieux intéressés, qui peuvent se prononcer par écrit dans un délai de trois mois.", i)
		if i == 5 {
			text += " La consultation est ouverte à tous, et ses résultats sont publiés avec le message du conseil fédéral."
		}
		save(fmt.Sprintf("fr%d", i), text)
	}
	save("or-only", "La procédure des cantons.")
	save("de-answer", "Bundesrat: Verfahren der Kantone.")
	return db
}

// filledSearch runs one search on the three lexical paths that run phrasings.
type filledSearch struct {
	alternates, keywords []string
	topK                 int
}

func (s filledSearch) searchText(t *testing.T, db *DB) []string {
	t.Helper()
	resp, err := db.GraphRAGTools().SearchText(context.Background(), ToolSearchTextRequest{
		Query: fillQuestion, Collection: "kb", TopK: s.topK, AlternateQueries: s.alternates, Keywords: s.keywords,
	})
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	var got []string
	for _, c := range resp.Chunks {
		got = append(got, c.DocumentID)
	}
	return got
}

func (s filledSearch) knowledge(t *testing.T, db *DB) []string {
	t.Helper()
	resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{
		Query: fillQuestion, Collection: "kb", TopK: s.topK, RetrievalMode: RetrievalModeLexical, AlternateQueries: s.alternates, Keywords: s.keywords,
	})
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	var got []string
	for _, h := range resp.Results {
		got = append(got, h.KnowledgeID)
	}
	return got
}

func (s filledSearch) textOnly(t *testing.T, db *DB) []string {
	t.Helper()
	results, err := db.SearchTextOnly(context.Background(), fillQuestion, TextSearchOptions{
		Collection: "kb", TopK: s.topK, AlternateQueries: s.alternates, Keywords: s.keywords,
	})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.DocID)
	}
	return got
}

// A plain question, with no alternate query and no keyword, runs as it
// always has. At top_k 5 it stops when it fills top_k: the short partial
// match does not enter. At top_k 10 its phrasings merge by their best
// weighted rank, which puts the partial match fifth.
func TestPlainQuestionStopsWhenItFillsTopK(t *testing.T) {
	db := openFilledStore(t)
	want := []string{"fr0", "fr1", "fr2", "fr3", "fr4"}
	s := filledSearch{topK: 5}
	if got := s.searchText(t, db); !slices.Equal(got, want) {
		t.Errorf("search_text: results %v, want %v", got, want)
	}
	if got := s.knowledge(t, db); !slices.Equal(got, want) {
		t.Errorf("SearchKnowledge: results %v, want %v", got, want)
	}
	want = []string{"fr0", "fr1", "fr2", "fr3", "or-only", "fr4", "fr5"}
	if got := (filledSearch{topK: 10}).searchText(t, db); !slices.Equal(got, want) {
		t.Errorf("search_text, top_k 10: results %v, want %v", got, want)
	}
}

// A variant that also finds a row the question ranks last lifts it to the
// top, at top_k 3 and 5, on each path: the variant ran, and its list counts
// by rank.
func TestAlternateLiftsARowTheQuestionAlsoFinds(t *testing.T) {
	db := openFilledStore(t)
	for _, k := range []int{3, 5} {
		s := filledSearch{alternates: []string{"procédure de consultation"}, topK: k}
		for name, got := range map[string][]string{"search_text": s.searchText(t, db), "SearchKnowledge": s.knowledge(t, db), "SearchTextOnly": s.textOnly(t, db)} {
			if len(got) != k || got[0] != "fr5" {
				t.Errorf("%s, top_k %d: results %v, want %d with fr5 first", name, k, got, k)
			}
		}
	}
	// The question's OR query runs after the variant: the partial match is
	// in a result of ten.
	s := filledSearch{alternates: []string{"procédure de consultation"}, topK: 10}
	for name, got := range map[string][]string{"search_text": s.searchText(t, db), "SearchTextOnly": s.textOnly(t, db)} {
		if !slices.Contains(got, "or-only") {
			t.Errorf("%s, top_k 10: results %v lack the row only the OR query finds", name, got)
		}
	}
}

// The same with a keyword instead of an alternate query: the keyword OR query
// runs too, and its list, where fr5 comes first, lifts fr5 from sixth into
// the five. One list of evidence against the question's two or three: it
// passes fr4, not fr0.
func TestKeywordLiftsARowTheQuestionAlsoFinds(t *testing.T) {
	db := openFilledStore(t)
	s := filledSearch{keywords: []string{"consultation"}, topK: 5}
	for name, got := range map[string][]string{"search_text": s.searchText(t, db), "SearchKnowledge": s.knowledge(t, db), "SearchTextOnly": s.textOnly(t, db)} {
		if len(got) != 5 || !slices.Contains(got, "fr5") || slices.Contains(got, "fr4") || got[0] != "fr0" {
			t.Errorf("%s: results %v, want five from fr0, with fr5 and without fr4", name, got)
		}
	}
}

// Each query's bm25 is measured against its own terms, so the lists of two
// queries merge by rank, not by score: rows that hold every word of the
// question are not passed by a short row that holds two, or by one that
// only the keywords find.
func TestPhrasingsMergeByRankNotByScore(t *testing.T) {
	db := openFilledStore(t)
	for _, k := range []int{3, 5} {
		s := filledSearch{keywords: []string{"Bundesrat", "Verfahren"}, topK: k}
		want := []string{"fr0", "fr1", "fr2", "fr3", "fr4"}[:k]
		for name, got := range map[string][]string{"search_text": s.searchText(t, db), "SearchTextOnly": s.textOnly(t, db)} {
			if !slices.Equal(got, want) {
				t.Errorf("%s, top_k %d: results %v, want %v", name, k, got, want)
			}
		}
	}
}

// A CJK question with keywords: the bigram path's list is fused with the
// others.
func TestCJKQuestionWithKeywordsKeepsItsBigramList(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "cjk.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{KnowledgeID: "zh", Content: "部署服务之前先构建镜像，再发布到集群。"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	resp, err := db.GraphRAGTools().SearchText(context.Background(), ToolSearchTextRequest{Query: "如何部署服务", TopK: 5, Keywords: []string{"kubernetes"}})
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	if len(resp.Chunks) != 1 || resp.Chunks[0].DocumentID != "zh" {
		t.Fatalf("chunks %+v, want the Chinese row", resp.Chunks)
	}
}

// The keyword OR query holds the question's words and the keywords. The
// question's own OR query, fused beside it, would count a row that holds a
// word of the question twice: "La procédure des cantons." would rank above
// the German row that holds all three keywords.
func TestFusedPhrasingsCountTheQuestionsWordsOnce(t *testing.T) {
	own := strings.Join(lexicalQueryKeywords(fillQuestion), " OR ")
	if got := phrasingQueries(fusedPhrasings(fillQuestion, nil, lexicalSearchQueries(fillQuestion, []string{"Bundesrat"}, nil, nil))); slices.Contains(got, own) || len(got) != 2 {
		t.Errorf("with a keyword: queries %q, want two, without %q", got, own)
	}
	if got := phrasingQueries(fusedPhrasings(fillQuestion, []string{"Bundesrat Verfahren Kantone"}, lexicalSearchQueries(fillQuestion, nil, []string{"Bundesrat Verfahren Kantone"}, nil))); !slices.Contains(got, own) {
		t.Errorf("with an alternate query only: queries %q lack the question's own OR query %q", got, own)
	}
	db := openFilledStore(t)
	s := filledSearch{keywords: []string{"Bundesrat", "Verfahren", "Kantone"}, topK: 10}
	for name, got := range map[string][]string{"search_text": s.searchText(t, db), "SearchTextOnly": s.textOnly(t, db)} {
		if a, b := slices.Index(got, "de-answer"), slices.Index(got, "or-only"); a < 0 || b < 0 || a > b {
			t.Errorf("%s: results %v, want de-answer above or-only", name, got)
		}
	}
}

// phrasingQueries are the queries of phrasings, in order.
func phrasingQueries(phrasings []lexicalPhrasing) []string {
	out := make([]string, len(phrasings))
	for i, p := range phrasings {
		out[i] = p.query
	}
	return out
}

// When every phrasing fails, SearchTextOnly reports the failure.
func TestSearchTextOnlyWithKeywordsReportsAFailedSearch(t *testing.T) {
	db := openFilledStore(t)
	_ = db.Close()
	if _, err := db.SearchTextOnly(context.Background(), fillQuestion, TextSearchOptions{Collection: "kb", TopK: 3, Keywords: []string{"consultation"}}); err == nil {
		t.Fatal("search on a closed store returned no error")
	}
}

// With keywords, SearchTextOnly applies Threshold to each phrasing's own
// normalized bm25, before the fusion: a row comes back when one phrasing
// scores it at the threshold or above. The scores it returns are the fused
// ranks, below any threshold of that scale.
func TestSearchTextOnlyAppliesThresholdBeforeFusion(t *testing.T) {
	db := openFilledStore(t)
	ctx := context.Background()
	keywords := []string{"consultation", "Bundesrat"}
	best := map[string]float64{}
	top := 0.0
	for _, q := range phrasingQueries(fusedPhrasings(fillQuestion, nil, lexicalSearchQueries(fillQuestion, keywords, nil, nil))) {
		rows, err := db.ftsSearch(ctx, q, TextSearchOptions{Collection: "kb", TopK: 40})
		if err != nil {
			t.Fatalf("ftsSearch %q: %v", q, err)
		}
		for _, r := range rows {
			top = max(top, r.Score)
			best[r.DocID] = max(best[r.DocID], r.Score)
		}
	}
	// Half the best score: the rows that hold the keywords pass, the rows
	// that hold only the question's words, which most rows contain, do not.
	threshold := top / 2
	var want []string
	for id, s := range best {
		if s >= threshold {
			want = append(want, id)
		}
	}
	slices.Sort(want)
	if len(want) == 0 || len(want) == len(best) {
		t.Fatalf("threshold %.3f keeps %d of %d rows; the test needs it to split them", threshold, len(want), len(best))
	}
	results, err := db.SearchTextOnly(ctx, fillQuestion, TextSearchOptions{Collection: "kb", TopK: 20, Threshold: threshold, Keywords: keywords})
	if err != nil {
		t.Fatalf("SearchTextOnly: %v", err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.DocID)
		if r.Score >= threshold {
			t.Errorf("%s scores %.3f, want a fused rank below the threshold %.3f", r.DocID, r.Score, threshold)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("threshold %.3f: results %v, want %v (the rows a phrasing scores at the threshold or above)", threshold, got, want)
	}
}

// The fusion constant is the usual 60: a row first in one list scores 1/61,
// first in two lists 2/61, and second in one list 1/62.
func TestLexicalFusionScoresByRankWithConstant60(t *testing.T) {
	a := []core.ScoredEmbedding{{Embedding: core.Embedding{ID: "x"}, Score: 9}, {Embedding: core.Embedding{ID: "y"}, Score: 8}}
	b := []core.ScoredEmbedding{{Embedding: core.Embedding{ID: "x"}, Score: 0.1}}
	got := fuseLexicalLists(0, lexicalList{rows: a, own: true}, lexicalList{rows: b, own: true})
	want := map[string]float64{"x": 2.0 / 61, "y": 1.0 / 62}
	if len(got) != 2 {
		t.Fatalf("fused %d rows, want 2", len(got))
	}
	for _, r := range got {
		if math.Abs(r.Score-want[r.ID]) > 1e-12 {
			t.Errorf("%s: score %.6f, want %.6f", r.ID, r.Score, want[r.ID])
		}
	}
}

// Each phrasing runs to four times top_k. x is fourth in every list (the
// three rows above it say alpha, or gamma, more often in fewer words), so at
// top_k 1 a list of three would leave it out; in lists of four it is in all
// four lists, and ranks first.
func TestPhrasingsRunToFourTimesTopK(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "depth.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	rows := map[string]string{
		"a1": "alpha alpha alpha.", "a2": "alpha alpha alpha alpha.", "a3": "alpha alpha alpha alpha alpha.",
		"g1": "gamma gamma gamma.", "g2": "gamma gamma gamma gamma.", "g3": "gamma gamma gamma gamma gamma.",
		"x": "alpha gamma, and a long tail of other words that make this row weigh less than the rest.",
	}
	for id, content := range rows {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	only, err := db.SearchTextOnly(ctx, "alpha", TextSearchOptions{TopK: 1, AlternateQueries: []string{"gamma"}})
	if err != nil || len(only) != 1 || only[0].DocID != "x" {
		t.Errorf("SearchTextOnly: %v, %v, want x", only, err)
	}
	resp, err := db.GraphRAGTools().SearchText(ctx, ToolSearchTextRequest{Query: "alpha", TopK: 1, AlternateQueries: []string{"gamma"}})
	if err != nil || len(resp.Chunks) != 1 || resp.Chunks[0].DocumentID != "x" {
		t.Errorf("search_text: %+v, %v, want x", resp, err)
	}
}

// The question's own lists count in full, an alternate query's or the
// keyword OR query's half. A row the question's own list puts first passes
// one that two variant lists put second: 1/61 against 2 × 0.5/62. Weighed
// the same, the variants would carry it, 2/62 against 1/61.
func TestLexicalFusionWeighsVariantListsHalf(t *testing.T) {
	row := func(id string) core.ScoredEmbedding { return core.ScoredEmbedding{Embedding: core.Embedding{ID: id}} }
	got := fuseLexicalLists(0,
		lexicalList{rows: []core.ScoredEmbedding{row("answer")}, own: true},
		lexicalList{rows: []core.ScoredEmbedding{row("x"), row("variant")}},
		lexicalList{rows: []core.ScoredEmbedding{row("y"), row("variant")}},
	)
	if len(got) != 4 || got[0].ID != "answer" {
		t.Fatalf("fused %v, want answer first", fusedIDs(got))
	}
	want := map[string]float64{"answer": 1.0 / 61, "variant": 1.0 / 62, "x": 0.5 / 61, "y": 0.5 / 61}
	for _, r := range got {
		if math.Abs(r.Score-want[r.ID]) > 1e-12 {
			t.Errorf("%s: score %.6f, want %.6f", r.ID, r.Score, want[r.ID])
		}
	}
	// The CJK bigram list of a question in two scripts counts for the share
	// of the question its CJK text is.
	half := fuseLexicalLists(0, lexicalList{rows: []core.ScoredEmbedding{row("cjk")}, own: true, share: 0.5})
	if math.Abs(half[0].Score-0.5/61) > 1e-12 {
		t.Errorf("a list of half the question: score %.6f, want %.6f", half[0].Score, 0.5/61)
	}
	if s := cjkShareOfQuestion("网站的备案号"); s != 1 {
		t.Errorf("share of a Chinese question: %v, want 1", s)
	}
	if s := cjkShareOfQuestion("harness release"); s != 0 {
		t.Errorf("share of an English question: %v, want 0", s)
	}
	if s := cjkShareOfQuestion("harness 仓库 crates"); s <= 0 || s >= 1 {
		t.Errorf("share of a question in two scripts: %v, want between 0 and 1", s)
	}
}

// Rows that score the same are ordered by their rank in the question's own
// lists, a row none of them holds after, not by id: "z-own", first in the
// question's list, scores 1/61, as "a-variant" does, first in two variant
// lists.
func TestLexicalFusionBreaksTiesByTheQuestionsOwnRank(t *testing.T) {
	row := func(id string) core.ScoredEmbedding { return core.ScoredEmbedding{Embedding: core.Embedding{ID: id}} }
	got := fuseLexicalLists(0,
		lexicalList{rows: []core.ScoredEmbedding{row("a-variant")}},
		lexicalList{rows: []core.ScoredEmbedding{row("a-variant")}},
		lexicalList{rows: []core.ScoredEmbedding{row("z-own")}, own: true},
	)
	if len(got) != 2 || got[0].Score != got[1].Score {
		t.Fatalf("fused %v, the test needs a tie", got)
	}
	if ids := fusedIDs(got); !slices.Equal(ids, []string{"z-own", "a-variant"}) {
		t.Errorf("fused %v, want z-own before a-variant", ids)
	}
}

func fusedIDs(rows []core.ScoredEmbedding) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// An alternate query whose every word is in the question adds no list: the
// question's OR query and its CJK bigrams search for those words already, and
// a row holding them would be counted twice. One with a word of its own runs.
func TestFusedPhrasingsLeaveOutAnAlternateOfTheQuestionsWords(t *testing.T) {
	question := "网站的工信部备案号和公安备案号分别是什么"
	alternates := []string{"网站 备案号", "website ICP filing number"}
	got := phrasingQueries(fusedPhrasings(question, alternates, lexicalSearchQueries(question, nil, alternates, nil)))
	if slices.Contains(got, sanitizeFTSQuery("网站 备案号")) || !slices.Contains(got, sanitizeFTSQuery("website ICP filing number")) {
		t.Errorf("queries %q, want the English variant and not the one of the question's words", got)
	}
	if !wordsOfQuestion("Conseil procédure", "procedure conseil") || wordsOfQuestion(fillQuestion, "procédure de consultation") {
		t.Error("wordsOfQuestion: want accents folded, and a word of its own to count")
	}
}

// SearchKnowledge's keyword floor runs after the cut to top_k. The rows that
// pass it fill top_k first: the short rows that hold only the keyword rank
// first in the keyword OR query, and would have filled the three and been
// dropped, leaving nothing.
func TestKnowledgeFloorFillsTopKBeforeTheCut(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "floor.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	save := func(id, content string) {
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: id, Content: content, Collection: "kb"}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	for i := 0; i < 4; i++ {
		save(fmt.Sprintf("only-keyword-%d", i), "Falcon falcon falcon.")
	}
	long := strings.Repeat(" Ringing records from the observatory, kept by volunteers, count every bird that passes the nets.", 6)
	save("answer-a", "The kestrel keeps to one migration path along the coast, year after year."+long)
	save("answer-b", "Each autumn the kestrel starts its migration a week later than the year before."+long)
	for i := 0; i < 8; i++ {
		save(fmt.Sprintf("filler-%d", i), fmt.Sprintf("Note %d on garden birds: sparrows, robins and wrens at the feeder.", i))
	}
	req := ToolSearchTextRequest{Query: "kestrel migration route", Collection: "kb", TopK: 3, Keywords: []string{"falcon"}}
	resp, err := db.GraphRAGTools().SearchText(ctx, req)
	if err != nil {
		t.Fatalf("search_text: %v", err)
	}
	for _, c := range resp.Chunks {
		if !strings.HasPrefix(c.DocumentID, "only-keyword-") {
			t.Fatalf("search_text top 3 %v: the test needs the keyword-only rows to fill it", chunkDocs(resp.Chunks))
		}
	}
	got, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: req.Query, Collection: "kb", TopK: 3, Keywords: req.Keywords, RetrievalMode: RetrievalModeLexical})
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	var ids []string
	for _, h := range got.Results {
		ids = append(ids, h.KnowledgeID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"answer-a", "answer-b"}) {
		t.Errorf("SearchKnowledge top 3: %v, want answer-a and answer-b", ids)
	}
}

func chunkDocs(chunks []ToolChunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.DocumentID
	}
	return out
}
