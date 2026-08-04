package skillopt

import (
	"strings"
)

// Sections is the parsed marker set of a lesson body. nugit's house lesson
// format (emitted by `nugit distill` and the capture template) is a run of bold
// lead-ins:
//
//	**Trigger:** <symptom>
//	**Insight:** <root cause + fix>
//	**Rejected:** <what not to do>
//	**Keywords:** a, b, c
//
// Hand-written lessons in real stores also use `## Trigger` headings, and some
// split the insight into `**Cause:**` / `**Fix:**`. ParseSections reads all of
// these; anything it does not recognise is dropped rather than guessed at.
type Sections struct {
	Trigger  string
	Insight  string
	Cause    string
	Fix      string
	Rejected string
	Keywords string
}

// Answer is the gold-answer text: the Insight when present, else the composed
// Cause + Fix pair (which is literally "root cause + fix"). Empty when the body
// carries neither.
func (s Sections) Answer() string {
	if s.Insight != "" {
		return s.Insight
	}
	switch {
	case s.Cause != "" && s.Fix != "":
		return s.Cause + "\n\n" + s.Fix
	case s.Fix != "":
		return s.Fix
	case s.Cause != "":
		return s.Cause
	}
	return ""
}

// knownMarkers maps a lower-cased marker name to the Sections field it fills.
var knownMarkers = map[string]func(*Sections, string){
	"trigger":  func(s *Sections, v string) { s.Trigger = v },
	"symptom":  func(s *Sections, v string) { s.Trigger = v },
	"insight":  func(s *Sections, v string) { s.Insight = v },
	"cause":    func(s *Sections, v string) { s.Cause = v },
	"why":      func(s *Sections, v string) { s.Cause = v }, // same slot: the root cause
	"fix":      func(s *Sections, v string) { s.Fix = v },
	"rejected": func(s *Sections, v string) { s.Rejected = v },
	"keywords": func(s *Sections, v string) { s.Keywords = v },
}

// ParseSections splits a lesson body into its marker sections. A section runs
// from its marker until the next marker (bold lead-in OR markdown heading) or
// the end of the body, so multi-line prose survives intact. Only the FIRST
// occurrence of a marker wins — a later restatement never silently overwrites
// the authored one.
func ParseSections(body string) Sections {
	var out Sections
	var (
		cur func(*Sections, string)
		buf []string
	)
	flush := func() {
		if cur != nil {
			cur(&out, unwrap(buf))
		}
		cur, buf = nil, nil
	}
	for _, line := range strings.Split(body, "\n") {
		name, rest, ok := markerAt(line)
		if !ok {
			if cur != nil {
				buf = append(buf, line)
			}
			continue
		}
		flush()
		if fn, known := knownMarkers[name]; known && !filled(&out, name) {
			cur = fn
			if rest != "" {
				buf = append(buf, rest)
			}
		}
	}
	flush()
	return out
}

// filled reports whether the named section already has text, so the first
// occurrence of a marker wins.
func filled(s *Sections, name string) bool {
	switch name {
	case "trigger", "symptom":
		return s.Trigger != ""
	case "insight":
		return s.Insight != ""
	case "cause", "why":
		return s.Cause != ""
	case "fix":
		return s.Fix != ""
	case "rejected":
		return s.Rejected != ""
	case "keywords":
		return s.Keywords != ""
	}
	return false
}

// markerAt recognises the two authored marker styles at the start of a line:
//
//	**Trigger:** text     /  **Trigger**: text   (bold lead-in)
//	## Trigger            /  ### Trigger         (heading)
//
// It returns the lower-cased marker name and any text that followed it on the
// same line. ok=false means the line is body text, not a marker.
func markerAt(line string) (name, rest string, ok bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "#") {
		h := strings.TrimSpace(strings.TrimLeft(t, "#"))
		h = strings.TrimSpace(strings.TrimSuffix(h, ":"))
		if h == "" {
			return "", "", false
		}
		// A heading is a marker boundary even when we don't know its name (an
		// unknown heading still ends the previous section).
		return strings.ToLower(h), "", true
	}
	if !strings.HasPrefix(t, "**") {
		return "", "", false
	}
	end := strings.Index(t[2:], "**")
	if end < 0 {
		return "", "", false
	}
	label := strings.TrimSpace(t[2 : 2+end])
	label = strings.TrimSpace(strings.TrimSuffix(label, ":"))
	if label == "" || strings.ContainsAny(label, ".!?") {
		return "", "", false // a bolded phrase mid-prose, not a lead-in
	}
	rest = strings.TrimSpace(t[2+end+2:])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	return strings.ToLower(label), rest, true
}

// unwrap joins hard-wrapped prose back into paragraphs: consecutive non-blank
// lines become one space-joined line, blank lines separate paragraphs. Markdown
// list items keep their own line so an enumerated insight stays readable.
func unwrap(lines []string) string {
	var paras []string
	var cur []string
	closePara := func() {
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			closePara()
			continue
		}
		if isListItem(t) {
			closePara()
			paras = append(paras, t)
			continue
		}
		cur = append(cur, t)
	}
	closePara()
	return strings.TrimSpace(strings.Join(paras, "\n\n"))
}

func isListItem(t string) bool {
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") {
		return true
	}
	for i := 0; i < len(t); i++ {
		if t[i] >= '0' && t[i] <= '9' {
			continue
		}
		return i > 0 && (t[i] == '.' || t[i] == ')') && i+1 < len(t) && t[i+1] == ' '
	}
	return false
}
