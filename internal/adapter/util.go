package adapter

import (
	"bufio"
	"os"
	"sort"
	"strings"
)

// tail returns the last n bytes of a file as lines, dropping a partial first
// line. Transcripts run to 2 MB+ and the statusline renders constantly, so
// reading the whole file every time was pure waste.
func tail(path string, n int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := fi.Size() - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil
	}
	buf := make([]byte, fi.Size()-start)
	got, _ := f.Read(buf)
	text := string(buf[:got])
	if start > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return strings.Split(text, "\n")
}

// head returns up to maxLines from the start of a file, reading at most n bytes.
// The startup prefix lives in the first few hundred lines.
func head(path string, n int64, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	read := int64(0)
	for sc.Scan() && len(out) < maxLines && read < n {
		line := sc.Text()
		read += int64(len(line))
		out = append(out, line)
	}
	return out
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
}

// WindowFor returns 0 when the model is unknown. Callers MUST treat 0 as
// "say nothing" rather than substituting a default — the previous
// implementation's silent 200K fallback made it fire at 5x too low.
func WindowFor(model string) int {
	if v := os.Getenv("CTXGUARD_LIMIT"); v != "" {
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	s := strings.ToLower(model)
	switch {
	case s == "" || synthetic.MatchString(s):
		return 0
	case strings.Contains(s, "[1m]"), strings.Contains(s, "1m"):
		return 1_000_000
	case strings.Contains(s, "haiku"):
		return 200_000
	case strings.Contains(s, "sonnet"), strings.Contains(s, "opus"), strings.Contains(s, "fable"):
		return 1_000_000
	}
	return 0
}
