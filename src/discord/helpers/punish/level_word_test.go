package punish

import "testing"

func TestLevelByWord(t *testing.T) {
	lv := func(key, name string) keyedLevel { return keyedLevel{Key: key, ladderLevel: ladderLevel{Name: name}} }
	levels := []keyedLevel{lv("a", "1.1 Discrimination"), lv("b", "1.10 NSFW Content"), lv("c", "1.2 Chat Spamming & Flooding")}
	for word, want := range map[string]string{"1.1": "a", "1.10": "b", "discrimination": "a", "nsfw": "b", "flooding": "c", "chat": "c", "the": "", "1.3": ""} {
		if got := levelByWord(levels, word); got != want {
			t.Errorf("%q: got %q want %q", word, got, want)
		}
	}
}
