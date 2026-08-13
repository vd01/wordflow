package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// newTestReadingService opens a ReadingService against a temp SQLite DB.
func newTestReadingService(t *testing.T) *ReadingService {
	t.Helper()
	r := &ReadingService{}
	if err := r.openDBAt(filepath.Join(t.TempDir(), "reading.db")); err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { r.db.Close() })
	return r
}

func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Hello world", []string{"Hello world"}},
		{"Line one\nLine two", []string{"Line one Line two"}}, // hard-wrapped lines join
		{"Para one.\n\nPara two.", []string{"Para one.", "Para two."}},
		{"A\n\n\nB", []string{"A", "B"}},
		{"\r\nWindows\r\n\r\nlines\r\n", []string{"Windows", "lines"}},
		{"", nil},
		{"   \n \n", nil},
	}
	for _, c := range cases {
		got := splitParagraphs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitParagraphs(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitParagraphs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestCreateAndListMaterial(t *testing.T) {
	r := newTestReadingService(t)
	id, err := r.CreateMaterial("My Title", "The quick brown fox.\n\nJumps over the lazy dog.")
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}
	if id == "" {
		t.Fatal("CreateMaterial returned empty id")
	}

	mats, err := r.GetMaterials()
	if err != nil {
		t.Fatalf("GetMaterials: %v", err)
	}
	if len(mats) != 1 {
		t.Fatalf("expected 1 material, got %d", len(mats))
	}
	if mats[0].Title != "My Title" {
		t.Errorf("title = %q", mats[0].Title)
	}
	if mats[0].Content != "" {
		t.Errorf("GetMaterials should not ship content, got %d chars", len(mats[0].Content))
	}
	// 9 words: The quick brown fox / Jumps over the lazy dog
	if mats[0].WordCount != 9 {
		t.Errorf("wordCount = %d, want 9", mats[0].WordCount)
	}

	one, err := r.GetMaterial(id)
	if err != nil {
		t.Fatalf("GetMaterial: %v", err)
	}
	if one.Content == "" || one.WordCount != 9 {
		t.Errorf("GetMaterial = %+v", one)
	}
}

func TestSetMarkEscalation(t *testing.T) {
	r := newTestReadingService(t)
	id, err := r.CreateMaterial("T", "Hello world hello")
	if err != nil {
		t.Fatal(err)
	}

	// Looked-up mark
	if err := r.SetMark(id, "HELLO", MarkLookedUp); err != nil {
		t.Fatalf("SetMark: %v", err)
	}
	marks, _ := r.GetMarks(id)
	if len(marks) != 1 || marks[0].Word != "hello" || marks[0].Status != MarkLookedUp {
		t.Fatalf("marks after lookup = %+v", marks)
	}

	// Save escalates 1 → 2
	if err := r.SetMark(id, "Hello", MarkSaved); err != nil {
		t.Fatal(err)
	}
	marks, _ = r.GetMarks(id)
	if marks[0].Status != MarkSaved {
		t.Fatalf("saved should win, got %+v", marks[0])
	}

	// Saved stays saved (no downgrade)
	if err := r.SetMark(id, "hello", MarkLookedUp); err != nil {
		t.Fatal(err)
	}
	marks, _ = r.GetMarks(id)
	if marks[0].Status != MarkSaved {
		t.Fatalf("looked-up must not downgrade saved, got %+v", marks[0])
	}

	// Saved count in list
	mats, _ := r.GetMaterials()
	if mats[0].SavedCount != 1 {
		t.Errorf("SavedCount = %d, want 1", mats[0].SavedCount)
	}
}

func TestUpdateMaterialPrunesOrphansAndTranslations(t *testing.T) {
	r := newTestReadingService(t)
	id, _ := r.CreateMaterial("T", "Alpha beta gamma.\n\nDelta epsilon.")
	_ = r.SetMark(id, "alpha", MarkLookedUp)
	_ = r.SetMark(id, "beta", MarkSaved)
	_ = r.SetMark(id, "omega", MarkLookedUp) // not in content → orphan

	// Simulate a cached translation for paragraph 1 ("Delta epsilon.")
	if _, err := r.db.Exec(
		"INSERT INTO translations (material_id, paragraph_index, translation, created_at) VALUES (?, 1, '翻译一', 0)",
		id,
	); err != nil {
		t.Fatal(err)
	}
	// Simulate a cached translation for paragraph 0 which will change.
	if _, err := r.db.Exec(
		"INSERT INTO translations (material_id, paragraph_index, translation, created_at) VALUES (?, 0, '旧翻译', 0)",
		id,
	); err != nil {
		t.Fatal(err)
	}

	// Edit: paragraph 0 changes, paragraph 1 unchanged. New content contains
	// "beta" but not "alpha" (so the alpha mark is an orphan to prune).
	if err := r.UpdateMaterial(id, "T", "Beta zeta changed.\n\nDelta epsilon."); err != nil {
		t.Fatalf("UpdateMaterial: %v", err)
	}

	marks, _ := r.GetMarks(id)
	got := map[string]int{}
	for _, m := range marks {
		got[m.Word] = m.Status
	}
	if _, ok := got["alpha"]; ok {
		t.Errorf("orphan mark 'alpha' not pruned: %+v", got)
	}
	if got["beta"] != MarkSaved {
		t.Errorf("mark 'beta' should survive edit: %+v", got)
	}
	if _, ok := got["omega"]; ok {
		t.Errorf("orphan mark 'omega' not pruned: %+v", got)
	}

	// Paragraph 0 translation invalidated, paragraph 1 kept.
	trs, _ := r.GetTranslations(id)
	if len(trs) != 1 || trs[0].ParagraphIndex != 1 || trs[0].Translation != "翻译一" {
		t.Errorf("translations after edit = %+v", trs)
	}
}

func TestDeleteMaterialCascades(t *testing.T) {
	r := newTestReadingService(t)
	id, _ := r.CreateMaterial("T", "Content here.")
	_ = r.SetMark(id, "content", MarkSaved)
	_, _ = r.db.Exec(
		"INSERT INTO translations (material_id, paragraph_index, translation, created_at) VALUES (?, 0, 'x', 0)", id,
	)
	_, _ = r.db.Exec(
		"INSERT INTO chat_messages (id, material_id, role, content, created_at) VALUES ('m1', ?, 'user', 'q', 0)", id,
	)

	if err := r.DeleteMaterial(id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetMaterial(id); err == nil {
		t.Error("material should be gone")
	}
	m, _ := r.GetMarks(id)
	if len(m) != 0 {
		t.Errorf("marks not cascaded: %+v", m)
	}
	tr, _ := r.GetTranslations(id)
	if len(tr) != 0 {
		t.Errorf("translations not cascaded: %+v", tr)
	}
	ch := r.loadChatHistory(id)
	if len(ch) != 0 {
		t.Errorf("chat not cascaded: %+v", ch)
	}
}

func TestTranslateParagraphCacheHit(t *testing.T) {
	r := newTestReadingService(t)
	id, _ := r.CreateMaterial("T", "Hello world.")
	// Seed a cached translation directly; TranslateParagraph must return it
	// without any LLM call (r.dict is nil here — a miss would panic).
	if _, err := r.db.Exec(
		"INSERT INTO translations (material_id, paragraph_index, translation, created_at) VALUES (?, 0, '你好世界', 0)",
		id,
	); err != nil {
		t.Fatal(err)
	}
	got, err := r.TranslateParagraph(id, 0, "Hello world.")
	if err != nil {
		t.Fatalf("TranslateParagraph: %v", err)
	}
	if got != "你好世界" {
		t.Errorf("translation = %q", got)
	}
}

func TestTrimChatHistory(t *testing.T) {
	mk := func(n int) []ChatMsg {
		var h []ChatMsg
		for i := 0; i < n; i++ {
			h = append(h, ChatMsg{Role: "user", Content: strings.Repeat("q", 2000)})
			h = append(h, ChatMsg{Role: "assistant", Content: strings.Repeat("a", 2000)})
		}
		return h
	}

	// Small history: nothing trimmed.
	small := mk(1)
	if got := trimChatHistory("material", "q", small, 12000); len(got) != 2 {
		t.Errorf("small history trimmed to %d", len(got))
	}

	// Huge history: pairs dropped from the front, count even, most recent kept.
	big := mk(50) // 50 pairs × 4000 chars = 200K chars ≈ 66K tokens
	got := trimChatHistory("material", "q", big, 12000)
	if len(got) == 0 || len(got)%2 != 0 {
		t.Fatalf("trimmed length = %d (want even, non-zero)", len(got))
	}
	if got[len(got)-1] != big[len(big)-1] {
		t.Error("most recent message must be kept")
	}
	if got[0] != big[len(big)-len(got)] {
		t.Error("trimmed history must be a suffix of the full history")
	}

	// Lone message over budget: dropped entirely.
	lone := []ChatMsg{{Role: "user", Content: strings.Repeat("x", 100000)}}
	if got := trimChatHistory("material", "q", lone, 100); len(got) != 0 {
		t.Errorf("lone over-budget message should be dropped, got %d", len(got))
	}
}

func TestExtractWordSet(t *testing.T) {
	set := extractWordSet("Hello, don't STOP! It's a test's test.")
	for _, w := range []string{"hello", "don't", "stop", "it's", "test's", "test"} {
		if !set[w] {
			t.Errorf("missing word %q in %v", w, set)
		}
	}
}

func TestCharLimit(t *testing.T) {
	r := newTestReadingService(t)
	big := strings.Repeat("a", maxMaterialChars+1)
	if _, err := r.CreateMaterial("T", big); err == nil {
		t.Error("expected char-limit error")
	}
}
