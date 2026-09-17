package main

import "strings"

const END = "<END>"

// Category lists are ordered most useful first, so Flat() can take a prefix
// of each to stay under the 255-option cap of a single Choice. Hierarchical
// mode uses the full lists through speculative fan-out.
var categories = []struct {
	Name  string
	Desc  string
	Words []string
}{
	{"stop", "punctuation, or the end of the reply", []string{".", "?", "!", ",", END}},
	{"function", "pronouns, connectors, small words", []string{
		"me", "you", "it", "we", "they", "him", "her", "this", "that", "what", "who", "all",
		"not", "no", "yes", "and", "or", "but", "if", "then", "when", "where", "why", "how",
		"now", "soon", "later", "before", "after", "again", "always", "never", "here", "there",
		"up", "down", "in", "out", "on", "off", "with", "for", "to", "from", "of", "very", "much",
		"too", "also", "only", "maybe", "because", "so", "one", "two", "many", "some", "every",
		"ok", "please", "thank", "sorry", "ugh", "grr", "hmm", "ha", "oh", "same", "other", "each",
		"more", "less", "most", "first", "last", "next", "still", "yet", "than", "as", "like",
		"about", "into", "over", "under", "back", "away", "together", "alone", "enough", "almost",
	}},
	{"noun", "things, people, places, ideas", []string{
		"rock", "fire", "cave", "food", "water", "tree", "sun", "moon", "sky", "night", "day",
		"friend", "tribe", "man", "woman", "child", "dog", "hand", "head", "heart", "home", "land",
		"thing", "way", "word", "time", "bug", "code", "machine", "box", "light", "problem", "idea",
		"answer", "plan", "work", "job", "team", "boss", "money", "trouble", "pain", "war", "gift",
		"hunt", "fight", "feast", "winter", "morning", "tomorrow", "today", "story", "song", "dream",
		"sleep", "meat", "mammoth", "bear", "wolf", "bird", "fish", "river", "mountain", "stick",
		"spear", "bone", "skin", "eye", "foot", "belly", "blood", "rain", "snow", "wind", "storm",
		"smoke", "mud", "sand", "grass", "leaf", "root", "berry", "egg", "wheel", "boat", "path",
		"hill", "hole", "wall", "door", "bed", "pot", "knife", "drum", "picture", "name", "spirit",
		"magic", "luck", "question", "game", "truth", "lie", "user", "computer", "phone", "screen",
		"button", "list", "number", "letter", "book", "map", "key", "lock", "test", "error", "log",
		"file", "line", "loop", "memory", "power", "speed", "size", "part", "piece", "end", "start",
		"middle", "side", "top", "bottom", "front", "reason", "rule", "law", "chief", "elder", "hunter",
		"baby", "family", "stranger", "enemy", "brain", "mouth", "ear", "nose", "arm", "leg", "tooth",
		"body", "life", "death", "world", "sea", "ice", "cloud", "star", "earth", "hour", "year", "week",
		"season", "summer", "spring", "silence", "noise", "smell", "taste", "color", "shape", "shadow",
	}},
	{"verb", "actions and states", []string{
		"is", "was", "will", "can", "must", "do", "have", "go", "come", "see", "look", "make", "take",
		"give", "want", "need", "like", "know", "think", "say", "tell", "ask", "help", "fix", "break",
		"find", "lose", "get", "put", "keep", "use", "try", "learn", "teach", "show", "wait", "stop",
		"start", "run", "eat", "drink", "hunt", "kill", "hit", "throw", "build", "burn", "grow", "die",
		"live", "sit", "stand", "jump", "climb", "swim", "fall", "hold", "carry", "open", "close", "play",
		"laugh", "cry", "feel", "fear", "hide", "bring", "push", "pull", "cut", "dig", "cook", "wash",
		"sleep", "wake", "remember", "forget", "share", "steal", "win", "smash", "sing", "dance", "write",
		"read", "count", "test", "ship", "talk", "hear", "smell", "touch", "love", "hate", "listen", "watch",
		"walk", "move", "turn", "change", "check", "choose", "pick", "send", "call", "answer", "follow",
		"lead", "join", "leave", "return", "meet", "mean", "matter", "happen", "become", "seem", "stay",
		"work", "rest", "hurt", "heal", "save", "spend", "pay", "buy", "sell", "trade", "borrow", "owe",
		"add", "remove", "search", "print", "load", "crash", "hang", "freeze", "melt", "shine", "blow",
		"should", "could", "would", "may", "let", "made", "did", "went", "saw", "got", "said", "found",
	}},
	{"adjective", "qualities and descriptions", []string{
		"big", "small", "good", "bad", "strong", "weak", "fast", "slow", "old", "new", "hot", "cold",
		"hard", "soft", "dark", "bright", "happy", "sad", "angry", "scared", "hungry", "tired", "sick",
		"dead", "alive", "long", "short", "far", "near", "true", "wrong", "right", "best", "worst", "safe",
		"dangerous", "smart", "dumb", "brave", "lazy", "broken", "simple", "easy", "free", "busy", "full",
		"empty", "clean", "dirty", "wet", "dry", "loud", "quiet", "heavy", "sharp", "deep", "high", "low",
		"red", "black", "white", "green", "blue", "young", "wise", "silly", "kind", "mean", "rich", "poor",
		"ready", "done", "open", "closed", "lost", "found", "warm", "cool", "sweet", "bitter", "sour",
		"fresh", "rotten", "thick", "thin", "wide", "narrow", "round", "flat", "same", "different", "few",
		"much", "little", "great", "fine", "sure", "real", "fake", "slowly", "fast", "well", "hardly",
	}},
}

var punctuation = map[string]bool{".": true, "?": true, "!": true, ",": true}

type Vocab struct {
	Words      []string
	Class      map[string]string // word -> category name
	ByCategory map[string][]string
}

func dedupe(words []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// Full returns every word with its category, for hierarchical mode.
func Full() *Vocab {
	v := &Vocab{Class: map[string]string{}, ByCategory: map[string][]string{}}
	for _, c := range categories {
		for _, w := range dedupe(c.Words) {
			if _, dup := v.Class[w]; dup {
				continue
			}
			v.Class[w] = c.Name
			v.Words = append(v.Words, w)
			v.ByCategory[c.Name] = append(v.ByCategory[c.Name], w)
		}
	}
	return v
}

// Flat returns at most 255 words for a single Choice: all stop tokens plus a
// proportional prefix of every other category.
func Flat() *Vocab {
	full := Full()
	const capacity = 255
	budget := capacity - len(full.ByCategory["stop"])
	total := 0
	for name, ws := range full.ByCategory {
		if name != "stop" {
			total += len(ws)
		}
	}
	v := &Vocab{Class: map[string]string{}, ByCategory: map[string][]string{}}
	used := 0
	for _, c := range categories {
		ws := full.ByCategory[c.Name]
		n := len(ws)
		if c.Name != "stop" {
			n = budget * len(ws) / total
		}
		for _, w := range ws[:n] {
			v.Class[w] = c.Name
			v.Words = append(v.Words, w)
			v.ByCategory[c.Name] = append(v.ByCategory[c.Name], w)
		}
		used += n
	}
	if len(v.Words) > capacity {
		panic("flat vocab over 255")
	}
	return v
}

func criteriaFor(words []string) map[string]any {
	crit := map[string]any{}
	for _, w := range words {
		crit[w] = nil
	}
	if _, ok := crit[END]; ok {
		crit[END] = "the whole reply is complete, nothing more to say"
	}
	if _, ok := crit["."]; ok {
		crit["."] = "end of a sentence"
		crit["?"] = "end of a question"
		crit["!"] = "end of an excited sentence"
		crit[","] = "short pause inside a sentence"
	}
	return crit
}

func detokenize(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if w == END {
			break
		}
		if punctuation[w] {
			b.WriteString(w)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	out := []rune(b.String())
	capNext := true
	for i, r := range out {
		if capNext && r >= 'a' && r <= 'z' {
			out[i] = r - 32
			capNext = false
		}
		if r == '.' || r == '?' || r == '!' {
			capNext = true
		}
	}
	return string(out)
}
